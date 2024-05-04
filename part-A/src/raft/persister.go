package raft

import (
	"sync"
)

//
// 支持 Raft 和 kvraft 保存持久状态
// Raft 状态（日志等）和 k/v 服务器快照。
//

type Persister struct {
	mu        sync.Mutex
	raftstate []byte
	snapshot  []byte
}

func MakePersister() *Persister {
	return &Persister{}
}

func clone(orign []byte) (cur []byte) {
	cur = make([]byte, len(orign))
	copy(cur, orign)
	return
}

func (ps *Persister) Copy() (new_ps *Persister) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	new_ps = MakePersister()
	new_ps.snapshot = ps.snapshot
	new_ps.raftstate = ps.raftstate
	return
}

func (ps *Persister) ReadRaftState() (sta []byte) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	sta = clone(ps.raftstate)
	return
}

func (ps *Persister) RaftStateSize() (siz int) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	siz = len(ps.raftstate)
	return
}

// 将 Raft 状态和 K/V 快照一起保存为单个原子操作，
// 以帮助避免它们不同步。

func (ps *Persister) Save(raftstate []byte, snapshot []byte) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	ps.raftstate = clone(raftstate)
	ps.snapshot = clone(snapshot)
}

func (ps *Persister) ReadSnapshot() (snp []byte) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	snp = clone(ps.snapshot)
	return
}

func (ps *Persister) SnapshotSize() (siz int) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	siz = len(ps.snapshot)
	return
}
