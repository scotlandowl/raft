package raft

import (
	"part-B/src/labrpc"
	"sync"
	"sync/atomic"
	"time"
)

type Role string

const (
	Follower   Role = "Follower"
	Candidator Role = "Candidator"
	Leader     Role = "Leader"
)

type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int

	SnapshotValid bool
	Snapshot      []byte
	SnapshotTerm  int
	SnapshotIndex int
}

type Raft struct {
	mu              sync.Mutex
	peers           []*labrpc.ClientEnd
	persister       *Persister
	me              int
	dead            int32
	role            Role
	currentTerm     int
	votedFor        int
	electionStart   time.Time
	electionTimeout time.Duration

	log         []LogEntry
	matchIndex  []int
	nextIndex   []int
	commitIndex int
	lastApplied int
	applyCh     chan ApplyMsg
	applyCond   *sync.Cond
}

func (rf *Raft) persist() {

}

func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 {
		return
	}
}

func (rf *Raft) Snapshot(index int, snapshot []byte) {

}

// Leader 进行日志追加
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rf.role != Leader {
		return 0, 0, false
	}
	rf.log = append(rf.log, LogEntry{
		CommandValid: true,
		Command:      command,
		Term:         rf.currentTerm,
	})
	LOG(rf.me, rf.currentTerm, DLeader, "Leader Accept log [%d]T%d", len(rf.log)-1, rf.currentTerm)
	return len(rf.log) - 1, rf.currentTerm, true
}

// 测试程序在每个测试结束后不会停止 Raft 创建的 goroutine，
// 而是调用 Kill() 方法来通知这些 goroutine 停止。
// 测试中的代码可以使用 killed() 方法来检查是否调用了 Kill()。使用原子操作可以避免使用锁。
func (rf *Raft) Kill() {
	atomic.StoreInt32(&rf.dead, 1)
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}

// 检测当前是否是预期状态
func (rf *Raft) contextLostLocked(role Role, term int) bool {
	return !(rf.currentTerm == term && rf.role == role)
}

func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me

	// 初始化代码（PartA, PartB, PartC）
	rf.currentTerm = 0
	rf.role = Follower
	rf.votedFor = -1

	// 为避免日志中的边缘情况（corner cases）添加一个虚拟条目
	rf.log = append(rf.log, LogEntry{})

	// 初始化 leader 视图切片
	rf.matchIndex = make([]int, len(rf.peers))
	rf.nextIndex = make([]int, len(rf.peers))

	// 初始化用于日志应用的字段
	rf.applyCh = applyCh
	rf.applyCond = sync.NewCond(&rf.mu)

	// 从崩溃前持久化的状态中恢复
	rf.readPersist(persister.ReadRaftState())

	// 启动定时器协程以开始选举
	go rf.electionTicker()
	go rf.applyTicker()

	return rf
}
