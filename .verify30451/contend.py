"""Run the unchanged named SQL test with an observed restart-admission competitor."""
import fcntl
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import threading
import time

mode, log_name = sys.argv[1:]
assert mode in {"red", "green"}
lock_path = Path(os.environ["TMPDIR"]) / "mo-test-cluster-lifecycle.lock"
stop = threading.Event()
state = {}
errors = []


def holder():
    try:
        with lock_path.open("a+b") as lock:
            while not stop.is_set():
                try:
                    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    break
                except BlockingIOError:
                    stop.wait(0.01)
            if stop.is_set():
                return
            state["acquired"] = time.monotonic()
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
process = subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                           text=True, bufsize=1, start_new_session=True)
worker = None
starts = 0
waits = []
named_failure = named_success = deadline = False


def terminate():
    if process.poll() is None:
        try:
            os.killpg(process.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass


timer = threading.Timer(1800, terminate)
timer.start()
try:
    with Path(log_name).open("w") as output:
        for line in process.stdout:
            output.write(line)
            output.flush()
            print(line, end="", flush=True)
            if "MO_UT_SETUP" in line and "phase=service-start " in line and "status=ready" in line:
                starts += 1
                if starts == 1:
                    worker = threading.Thread(target=holder)
                    worker.start()
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
    try:
        process.wait(timeout=20)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGKILL)
        process.wait()
    process.stdout.close()
    stop.set()
    if worker is not None:
        worker.join(timeout=10)
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
