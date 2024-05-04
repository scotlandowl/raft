package raft

import (
	"bytes"
	"fmt"
	"part-C/src/labgob"
)

// 将 Raft 的持久状态保存到稳定存储介质中，
// 在崩溃后重启时可以检索到这些信息。
// 请参考论文中的图2，了解应该持久保存哪些信息。
// 在尚未实现快照功能之前，请将第二个参数传递为 nil 给 persister.Save()。
// 在实现了快照功能后，传递当前的快照（如果尚未存在快照，则传递 nil）。
func (rf *Raft) persistLocked() {
	w := new(bytes.Buffer)
	e := labgob.NewEncoder(w)

	e.Encode(rf.currentTerm)
	e.Encode(rf.votedFor)
	e.Encode(rf.log)
	raftstate := w.Bytes()
	rf.persister.Save(raftstate, nil)
	LOG(rf.me, rf.currentTerm, DPersist, "Persist: %v", rf.persistString())
}

// 恢复先前 持久化 了的状态
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 {
		return
	}
	r := bytes.NewBuffer(data)
	d := labgob.NewDecoder(r)

	var currentTerm int
	if err := d.Decode(&currentTerm); err != nil {
		LOG(rf.me, rf.currentTerm, DPersist, "Read currentTerm error: %v", err)
		return
	}
	rf.currentTerm = currentTerm

	var votedFor int
	if err := d.Decode(&votedFor); err != nil {
		LOG(rf.me, rf.currentTerm, DPersist, "Read votedFor error: %v", err)
		return
	}
	rf.votedFor = votedFor

	var log []LogEntry
	if err := d.Decode(&log); err != nil {
		LOG(rf.me, rf.currentTerm, DPersist, "Read log error: %v", err)
		return
	}
	rf.log = log
	LOG(rf.me, rf.currentTerm, DPersist, "Read from persist: %v", rf.persistString())
}

func (rf *Raft) persistString() string {
	return fmt.Sprintf("T%d, VoteFor: %d, Log: [0: %d)", rf.currentTerm, rf.votedFor, len(rf.log))
}
