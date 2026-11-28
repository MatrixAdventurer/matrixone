// Copyright 2026 Matrix Origin
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package checkers

import (
	"fmt"
	"testing"

	"github.com/matrixorigin/matrixone/pkg/hakeeper"
	"github.com/matrixorigin/matrixone/pkg/hakeeper/checkers/util"
	pb "github.com/matrixorigin/matrixone/pkg/pb/logservice"
	"github.com/matrixorigin/matrixone/pkg/pb/metadata"
	"github.com/stretchr/testify/require"
)

// Coordinator-level regressions for #32967 / review of #32984. They drive the
// real operator controller across several checks with simulated store reports.

type membershipCluster struct {
	t           *testing.T
	coordinator *Coordinator
	alloc       *util.TestIDAllocator
	tick        uint64
	voters      uint64
	nonVoting   uint64
	shard       pb.LogShardInfo
	live        []string
}

func newMembershipCluster(t *testing.T, voters, nonVoting uint64, members map[uint64]string, live ...string) *membershipCluster {
	return &membershipCluster{
		t:           t,
		coordinator: NewCoordinator("", hakeeper.Config{}),
		alloc:       util.NewTestIDAllocator(10),
		voters:      voters,
		nonVoting:   nonVoting,
		shard:       pb.LogShardInfo{ShardID: 1, Epoch: 1, LeaderID: 1, Term: 1, Replicas: members},
		live:        live,
	}
}

func (m *membershipCluster) state() pb.CheckerState {
	stores := make(map[string]pb.LogStoreInfo, len(m.live))
	for _, uuid := range m.live {
		store := pb.LogStoreInfo{Tick: m.tick}
		for replicaID, owner := range m.shard.Replicas {
			if owner == uuid {
				store.Replicas = append(store.Replicas, pb.LogReplicaInfo{LogShardInfo: m.shard, ReplicaID: replicaID})
			}
		}
		for replicaID, owner := range m.shard.NonVotingReplicas {
			if owner == uuid {
				store.Replicas = append(store.Replicas, pb.LogReplicaInfo{LogShardInfo: m.shard, ReplicaID: replicaID, IsNonVoting: true})
			}
		}
		stores[uuid] = store
	}
	return pb.CheckerState{
		Tick:                m.tick,
		ClusterInfo:         pb.ClusterInfo{LogShards: []metadata.LogShardRecord{{ShardID: 1, NumberOfReplicas: m.voters}}},
		LogState:            pb.LogState{Shards: map[uint64]pb.LogShardInfo{1: m.shard}, Stores: stores},
		NonVotingReplicaNum: m.nonVoting,
	}
}

// check runs one coordinator round and returns the shard-1 add commands.
func (m *membershipCluster) check() []pb.ConfigChange {
	m.tick++
	var adds []pb.ConfigChange
	for _, cmd := range m.coordinator.Check(m.alloc, m.state(), false) {
		if cmd.ConfigChange == nil || cmd.ConfigChange.Replica.ShardID != 1 {
			continue
		}
		switch cmd.ConfigChange.ChangeType {
		case pb.AddReplica, pb.AddNonVotingReplica:
			adds = append(adds, *cmd.ConfigChange)
		}
	}
	return adds
}

// admit simulates the configuration change committing: the shard epoch
// advances (each committed change bumps it) and the new member reports.
func (m *membershipCluster) admit(change pb.ConfigChange) {
	replicas := map[uint64]string{}
	for id, uuid := range m.shard.Replicas {
		replicas[id] = uuid
	}
	nonVoting := map[uint64]string{}
	for id, uuid := range m.shard.NonVotingReplicas {
		nonVoting[id] = uuid
	}
	if change.ChangeType == pb.AddNonVotingReplica {
		nonVoting[change.Replica.ReplicaID] = change.Replica.UUID
	} else {
		replicas[change.Replica.ReplicaID] = change.Replica.UUID
	}
	m.shard.Replicas, m.shard.NonVotingReplicas = replicas, nonVoting
	m.shard.Epoch += 2
}

func describe(adds []pb.ConfigChange) string {
	out := ""
	for _, add := range adds {
		out += fmt.Sprintf("%s:%d@%d ", add.Replica.UUID, add.Replica.ReplicaID, add.Replica.Epoch)
	}
	return out
}

// P1: two adds for the same shard must not be emitted with the same observed
// epoch. The second is issued only after the first is reflected, using the
// freshly observed configuration index.
func TestLogMembershipAddsAreSequencedWithFreshEpoch(t *testing.T) {
	m := newMembershipCluster(t, 4, 0, map[uint64]string{1: "a", 2: "b"}, "a", "b", "c", "d")
	first := m.check()
	require.Len(t, first, 1, "one membership change per shard per round, got %s", describe(first))
	require.Equal(t, uint64(1), first[0].Replica.Epoch)

	require.Empty(t, m.check(), "no further add while the first change is in flight")

	m.admit(first[0])
	second := m.check()
	require.Len(t, second, 1, "got %s", describe(second))
	require.NotEqual(t, first[0].Replica.UUID, second[0].Replica.UUID)
	require.Equal(t, m.shard.Epoch, second[0].Replica.Epoch, "the next add uses the observed config index")
}

// P2: a store that becomes available while an add is in flight must not cause
// a second add to the store that already owns the in-flight replica.
func TestLogMembershipInFlightAddReservesTarget(t *testing.T) {
	m := newMembershipCluster(t, 4, 0, map[uint64]string{1: "a", 2: "b"}, "a", "b", "c")
	first := m.check()
	require.Len(t, first, 1, "got %s", describe(first))
	require.Equal(t, "c", first[0].Replica.UUID)

	m.live = append(m.live, "d") // d becomes healthy before c's add is reported
	for _, add := range m.check() {
		require.NotEqual(t, "c", add.Replica.UUID, "c already owns the in-flight add: %s", describe([]pb.ConfigChange{add}))
	}

	m.admit(first[0])
	next := m.check()
	require.Len(t, next, 1, "got %s", describe(next))
	require.Equal(t, "d", next[0].Replica.UUID)
	require.Equal(t, m.shard.Epoch, next[0].Replica.Epoch)
}

// P2: simultaneous voting and non-voting demand on one shard must not schedule
// both roles to the same store, or two changes from one observed epoch.
func TestLogMembershipVotingAndNonVotingShareShardOwner(t *testing.T) {
	m := newMembershipCluster(t, 3, 1, map[uint64]string{1: "a", 2: "b"}, "a", "b", "c", "d")
	adds := m.check()
	require.LessOrEqual(t, len(adds), 1, "one membership change per shard across roles, got %s", describe(adds))
	require.Len(t, adds, 1)

	m.admit(adds[0])
	next := m.check()
	require.Len(t, next, 1, "got %s", describe(next))
	require.NotEqual(t, adds[0].Replica.UUID, next[0].Replica.UUID, "roles must not share a target store")
	require.NotEqual(t, adds[0].ChangeType, next[0].ChangeType)
	require.Equal(t, m.shard.Epoch, next[0].Replica.Epoch)
}

// A change rejected by Raft (its observed epoch went stale) must be retried
// with the newly observed configuration index.
func TestLogMembershipStaleAddIsRetriedWithFreshEpoch(t *testing.T) {
	m := newMembershipCluster(t, 4, 0, map[uint64]string{1: "a", 2: "b"}, "a", "b", "c")
	first := m.check()
	require.Len(t, first, 1, "got %s", describe(first))
	staleEpoch := first[0].Replica.Epoch

	m.shard.Epoch += 2 // another committed change; the add at staleEpoch is rejected
	var retry []pb.ConfigChange
	for round := 0; round < 3 && len(retry) == 0; round++ {
		retry = m.check()
	}
	require.Len(t, retry, 1, "stale add was not retried")
	require.Equal(t, first[0].Replica.UUID, retry[0].Replica.UUID)
	require.Equal(t, m.shard.Epoch, retry[0].Replica.Epoch)
	require.NotEqual(t, staleEpoch, retry[0].Replica.Epoch)
}
