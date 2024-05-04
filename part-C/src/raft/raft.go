package raft

import (
	"fmt"
	"part-C/src/labrpc"
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

const (
	tickTime            = 100 * time.Millisecond
	electionTimeoutMin  = 5 * tickTime
	electionTimeoutMax  = 10 * tickTime
	replicationInterval = 2 * tickTime
)

const (
	InvalidIndex int = 0
	InvalidTerm  int = 0
)

// 状态切换
func (rf *Raft) becomeFollowerLocked(term int) {
	// 对方 term 小于己方 term，拒绝
	if term < rf.currentTerm {
		LOG(rf.me, rf.currentTerm, DError, "Can't become follower, lower term: T%d", term)
		return
	}
	LOG(rf.me, rf.currentTerm, DLog, "%s -> Follower at term: T%v->T%v", rf.role, rf.currentTerm, term)
	rf.role = Follower
	shouldPersist := rf.currentTerm != term
	// 投票
	if term > rf.currentTerm {
		rf.votedFor = -1
	}
	rf.currentTerm = term
	if shouldPersist {
		rf.persistLocked()
	}
}

func (rf *Raft) becomeCandidateLocked() {
	// 判断身份
	if rf.role == Leader {
		LOG(rf.me, rf.currentTerm, DError, "Leader can't become candidator")
		return
	}

	// term 自增，更改身份，给自己投票
	LOG(rf.me, rf.currentTerm, DVote, "%s -> Candidator at term: T%v", rf.role, rf.currentTerm+1)
	rf.currentTerm++
	rf.role = Candidator
	rf.votedFor = rf.me
	rf.persistLocked()
}

func (rf *Raft) becomeLeaderLocked() {
	// 判断身份
	if rf.role != Candidator {
		LOG(rf.me, rf.currentTerm, DError, "Only Candidator can become Leader")
		return
	}

	LOG(rf.me, rf.currentTerm, DLeader, "Become Leader at term: T%v", rf.currentTerm)
	rf.role = Leader

	// nextIndex array initialized to leader last log index + 1
	for peer := 0; peer < len(rf.peers); peer++ {
		rf.nextIndex[peer] = len(rf.log)
		rf.matchIndex[peer] = 0
	}
}

func (rf *Raft) firstLogFor(term int) int {
	for idx, entry := range rf.log {
		if entry.Term == term {
			return idx
		} else if entry.Term > term {
			break
		}
	}
	return InvalidIndex
}

func (rf *Raft) logString() string {
	var terms string
	prevTerm := rf.log[0].Term
	prevStart := 0
	for i := 0; i < len(rf.log); i++ {
		if rf.log[i].Term != prevTerm {
			terms += fmt.Sprintf(" [%d, %d]T%d", prevStart, i-1, prevTerm)
			prevTerm = rf.log[i].Term
			prevStart = i
		}
	}
	terms += fmt.Sprintf(" [%d, %d]T%d", prevStart, len(rf.log)-1, prevTerm)
	return terms
}

// 返回当前任期号以及该节点是否认为自己是 Leader
func (rf *Raft) GetState() (int, bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.currentTerm, rf.role == Leader
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
	rf.persistLocked()
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
