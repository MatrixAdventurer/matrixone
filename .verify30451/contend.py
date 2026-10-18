"""Run the named SQL test with a temporary, bounded restart-admission handshake.

The handshake only orders fault injection after Close and before Start. Original
source is restored before the normal owning-package, race and static gates.
"""
import fcntl
import difflib
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import socket
import subprocess
import sys
import threading
import time

mode, log_name = sys.argv[1:]
assert mode in {"red", "green"}
lock_path = Path(os.environ["TMPDIR"]) / "mo-test-cluster-lifecycle.lock"
socket_path = str(Path(os.environ["TMPDIR"]) / "restart-barrier.sock")
source_path = Path("pkg/tests/sqlintegration/multicn/view_description_test.go")
original_bytes = source_path.read_bytes()
original_source = original_bytes.decode()
anchor = "\trequire.NoError(t, cluster.Close())\n"
assert original_source.count(anchor) == 1
handshake = '''\tif endpoint := os.Getenv("MO30451_BARRIER"); endpoint != "" {
\t\tconnection, err := net.DialTimeout("unix", endpoint, 5*time.Second)
\t\trequire.NoError(t, err)
\t\trequire.NoError(t, connection.SetDeadline(time.Now().Add(10*time.Second)))
\t\tack := make([]byte, 1)
\t\t_, err = io.ReadFull(connection, ack)
\t\trequire.NoError(t, connection.Close())
\t\trequire.NoError(t, err)
\t\trequire.Equal(t, byte(1), ack[0])
\t}
'''
instrumented_source = original_source.replace('import (\n', 'import (\n\t"io"\n\t"net"\n\t"os"\n', 1)
instrumented_source = instrumented_source.replace(anchor, anchor + handshake)
Path(log_name + ".instrumentation.patch").write_text("".join(difflib.unified_diff(
    original_source.splitlines(True), instrumented_source.splitlines(True),
    fromfile=str(source_path), tofile=str(source_path) + ".instrumented")))
Path(log_name + ".source-sha256").write_text(hashlib.sha256(original_bytes).hexdigest())
listener = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
listener.bind(socket_path)
listener.listen(1)
listener.settimeout(0.2)
stop = threading.Event()
state = {}
errors = []


def holder():
    try:
        while not stop.is_set():
            try:
                connection, _ = listener.accept()
                break
            except socket.timeout:
                continue
        else:
            return
        with connection, lock_path.open("a+b") as lock:
            # Close has released the lock; a failed acquisition is inconclusive.
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            state["acquired"] = time.monotonic()
            connection.settimeout(10)
            connection.sendall(b"\x01")
            # Duration is the injected fault, not a synchronization shortcut.
            stop.wait(185)
            state["held_seconds"] = time.monotonic() - state["acquired"]
            fcntl.flock(lock, fcntl.LOCK_UN)
    except BaseException as error:
        errors.append(repr(error))


def duration(value):
    units = {"ns": 1e-9, "us": 1e-6, "µs": 1e-6, "ms": 1e-3,
             "s": 1, "m": 60, "h": 3600}
    return sum(float(number) * units[unit]
               for number, unit in re.findall(r"([\d.]+)(ns|us|µs|ms|s|m|h)", value))


command = [".agents/skills/mo-dev/scripts/mo-cgo-test", "-count=1", "-vet=all",
           "-short", "-tags=matrixone_test", "-timeout=15m", "-v",
           "-run=^TestViewDescriptionTwoCN$", "./pkg/tests/sqlintegration/multicn"]
process = None
worker = threading.Thread(target=holder)
starts = 0
waits = []
named_failure = named_success = deadline = False


def terminate():
    if process is not None and process.poll() is None:
        try:
            os.killpg(process.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass


timer = threading.Timer(1800, terminate)
try:
    source_path.write_text(instrumented_source)
    worker.start()
    process = subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                               text=True, bufsize=1, start_new_session=True,
                               env={**os.environ, "MO30451_BARRIER": socket_path})
    timer.start()
    with Path(log_name).open("w") as output:
        for line in process.stdout:
            output.write(line)
            output.flush()
            print(line, end="", flush=True)
            if "MO_UT_SETUP" in line and "phase=service-start " in line and "status=ready" in line:
                starts += 1
            if "MO_UT_SETUP" in line and "phase=admission-acquire " in line and "status=ready" in line:
                match = re.search(r"duration=([^ ]+)", line)
                if match:
                    waits.append(duration(match.group(1)))
            named_failure |= "--- FAIL: TestViewDescriptionTwoCN" in line
            named_success |= "--- PASS: TestViewDescriptionTwoCN" in line
            deadline |= "context deadline exceeded" in line
    result = process.wait()
finally:
    timer.cancel()
    terminate()
    if process is not None:
        try:
            process.wait(timeout=20)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait()
        process.stdout.close()
    stop.set()
    if worker.ident is not None:
        worker.join(timeout=10)
    listener.close()
    Path(socket_path).unlink(missing_ok=True)
    source_path.write_bytes(original_bytes)
    assert source_path.read_bytes() == original_bytes, "source restoration changed bytes"
    assert not worker.is_alive(), "validation lock holder did not terminate"

summary = dict(mode=mode, exit_code=result, starts=starts, waits=waits,
               holder=state, errors=errors)
Path(log_name + ".summary.json").write_text(json.dumps(summary, indent=2))
print("CONTROL_SUMMARY", json.dumps(summary), flush=True)
assert not errors, errors
assert state.get("held_seconds", 0) >= 184.9, "holder did not retain the injected fault"
assert len(waits) >= 2 and waits[-1] >= 180, "restart missed contention: inconclusive"
assert starts >= 2, "restart itself did not reach readiness"
if mode == "red":
    assert result != 0 and named_failure and deadline, "not the expected query-context regression"
else:
    assert result == 0 and named_success and not named_failure, "fixed metadata/persistence oracle failed"
