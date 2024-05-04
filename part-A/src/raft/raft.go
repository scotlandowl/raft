package raft

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"part-A/src/labrpc"
)

//
// 公开的 API 概述
// rf = Make(...)
//   创建一个新的 Raft 服务器。
// rf.Start(command interface{}) (index, term, isleader)
//   启动对新日志条目的协议达成一致。
// rf.GetState() (term, isLeader)
//   请求 Raft 的当前任期以及它是否认为自己是领导者。
// ApplyMsg
//   每当将新条目提交到日志中时，每个 Raft 对等方
//   都应向服务（或测试人员）发送一个 ApplyMsg
//   在相同的服务器中。
//

// 当每个 Raft 对等方意识到连续的日志条目已提交时，
// 该对等方应通过传递给 Make() 的 applyCh 将 ApplyMsg
// 发送到服务（或测试人员）在同一台服务器上。
// 将 CommandValid 设置为 true 表示 ApplyMsg 包含一个新提交的日志条目。

// 在第四部分中，您将希望在 applyCh 上发送其他类型的消息（例如快照），
// 但是对于这些其他用途，将 CommandValid 设置为 false。

type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int

	SnapshotValid bool
	Snapshot      []byte
	SnapshotTerm  int
	SnapshotIndex int
}

const (
	tickTime           = time.Millisecond * 100
	ElectionTimeoutMax = tickTime * 10
	ElectionTimeoutMin = tickTime * 5
	HeartbeatInterval  = tickTime * 1
)

func (rf *Raft) isElectionTimeoutLocked() bool {
	return rf.electionStart.Add(rf.electionTimeout).Before(time.Now())
}

func (rf *Raft) resetElectionTimeoutLocked() {
	rf.electionStart = time.Now()
	randRange := int64(ElectionTimeoutMax - ElectionTimeoutMin)
	rf.electionTimeout = ElectionTimeoutMin + time.Duration(rand.Int63()%randRange)
}

type Role string

const (
	Leader     Role = "Leader"
	Follower   Role = "Follower"
	Candidator Role = "Candidator"
)

// 单个 raft 节点
type Raft struct {
	mu        sync.Mutex
	peers     []*labrpc.ClientEnd
	persister *Persister
	me        int // 当前节点的 index
	dead      int32

	currentTerm int
	role        Role
	votedFor    int

	electionTimeout time.Duration
	electionStart   time.Time
}

func (rf *Raft) GetState() (curTerm int, isLeader bool) {
	curTerm, isLeader = rf.currentTerm, rf.role == Leader
	return
}

func (rf *Raft) becomeFollowerLocked(term int) {
	// 自己 term 更大，不予理睬
	if term < rf.currentTerm {
		LOG(rf.me, rf.currentTerm, DError, "can't become follower, lower term: T%d", term)
		return
	}
	// 转变为 Follower
	LOG(rf.me, rf.currentTerm, DLog, "%s -> Follower at term: T%d->T%d", rf.role, rf.currentTerm, term)
	rf.role = Follower
	// 倘若发起方的 term 比自己更高， 清空自己的投票权
	if term > rf.currentTerm {
		rf.votedFor = -1
	}
	// 更新自己已知的最高 term
	rf.currentTerm = term
}

func (rf *Raft) becomeCandidatorLocked() {
	if rf.role == Leader {
		LOG(rf.me, rf.currentTerm, DError, "Leader Can't become Candidator")
		return
	}
	LOG(rf.me, rf.currentTerm, DVote, "%s -> Candidator at term: T%d", rf.role, rf.currentTerm+1)
	// term 自增
	rf.currentTerm++
	rf.role = Candidator
	// 立刻给自己投一票
	rf.votedFor = rf.me
}

func (rf *Raft) becomeLeaderLocked() {
	if rf.role != Candidator {
		LOG(rf.me, rf.currentTerm, DError, "Only Candidator can become Leader")
		return
	}
	LOG(rf.me, rf.currentTerm, DLeader, "Leader at term: T%d", rf.currentTerm)
	rf.role = Leader
}

// 将 Raft 的持久状态保存到稳定存储介质中
func (rf *Raft) persist() {

}

// 恢复之前持久化的状态
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 {
		return
	}
}

// 实现 Snapshot
func (rf *Raft) Snapshot(index int, snapshot []byte) {

}

// RequestVote RPC 的参数结构。
type RequestVoteArgs struct {
	Term       int
	Candidator int
}

// RequestVote RPC 回复结构
type RequestVoteReply struct {
	Term        int
	GrantedVote bool
}

func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	reply.Term = rf.currentTerm

	if args.Term < rf.currentTerm {
		LOG(rf.me, rf.currentTerm, DVote, "-> S%d, Reject Vote Request, Higher Term: T%d > T%d", args.Candidator, args.Term, rf.currentTerm, args.Term)
		reply.GrantedVote = false
		return
	}

	// candidator 的 term 比自己的大，更新自己的 term
	if args.Term > rf.currentTerm {
		rf.becomeFollowerLocked(args.Term)
	}

	// 以及进行过投票
	if rf.votedFor != -1 {
		LOG(rf.me, rf.currentTerm, DVote, "-> S%d, Reject, Already voted to S%d", args.Candidator, rf.votedFor)
		return
	}

	// 投票，并更新自己竞选超时时间
	reply.GrantedVote = true
	rf.votedFor = args.Candidator
	rf.resetElectionTimeoutLocked()
	LOG(rf.me, rf.currentTerm, DVote, "-> S%d, Vote Granted", args.Candidator)
}

func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if args.Term < rf.currentTerm {
		LOG(rf.me, rf.currentTerm, DLog, "-> S%d, Reject AppendEntries, Higher Term: T%d > T%d", args.LeaderId, args.Term, rf.currentTerm)
		return
	}

	if args.Term > rf.currentTerm {
		rf.becomeFollowerLocked(args.Term)
	}

	reply.Success = true
	rf.resetElectionTimeoutLocked()
}

func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}

// 第一个返回值是命令将被提交的索引位置。
// 第二个返回值是当前任期。
// 第三个返回值是如果该服务器认为自己是领导者则为 true。
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	index := -1
	term := -1
	isLeader := true

	return index, term, isLeader
}

// 测试器在每次测试后不会停止 Raft 创建的 goroutine，但会调用 Kill() 方法。
// 你的代码可以使用 killed() 方法来检查是否已调用 Kill()。
// 使用 atomic 可以避免使用锁的需要。
// 问题在于长时间运行的 goroutine 会占用内存并消耗 CPU 时间，
// 可能导致后续测试失败并生成混乱的调试输出。
// 任何具有长时间运行循环的 goroutine 应当调用 killed() 方法来检查是否应该停止。
func (rf *Raft) Kill() {
	atomic.StoreInt32(&rf.dead, 1)
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}

func (rf *Raft) electionTicker() {
	for !rf.killed() {
		// 这里加锁是因为在判断过程中，可能有其他线程修改了role，导致判断错误。
		func() {
			rf.mu.Lock()
			defer rf.mu.Unlock()
			if rf.role != Leader && rf.isElectionTimeoutLocked() {
				rf.becomeCandidatorLocked()
				go rf.startElection(rf.currentTerm)
			}
		}()
		ms := 50 + (rand.Int63() % 300)
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
}

func (rf *Raft) startElection(term int) bool {
	// 总票数
	votes := 0
	askVoteFromPeer := func(peer int, args *RequestVoteArgs) {
		reply := &RequestVoteReply{}
		// 调用 sendRequestVote RPC
		ok := rf.sendRequestVote(peer, args, reply)

		// 防止 rf 任期号和身份被修改需要加锁
		rf.mu.Lock()
		defer rf.mu.Unlock()

		if !ok {
			LOG(rf.me, rf.currentTerm, DDebug, "Ask Vote From S%d Failed, Lost Or error", peer)
			return
		}

		// 对方 term 更大
		if reply.Term > rf.currentTerm {
			rf.becomeFollowerLocked(reply.Term)
			return
		}

		// 确保投票期间候选人状态正确
		if rf.CheckContextLocked(Candidator, term) {
			LOG(rf.me, rf.currentTerm, DVote, "Lost Context, abort RequestVoteReply from S%d", peer)
			return
		}

		// 收到有效的投票
		if reply.GrantedVote {
			votes++
			if votes > len(rf.peers)/2 {
				rf.becomeLeaderLocked()
				LOG(rf.me, rf.currentTerm, DLeader, "S%d Become Leader at term: T%d", rf.me, rf.currentTerm)
				// 启动心跳
				go rf.replicationTicker(term)
			}
		}
	}

	rf.mu.Lock()
	defer rf.mu.Unlock()

	// 如果当前节点已经是 candidator 或者 已经有其他节点正在竞选，则不再发起选举
	if rf.CheckContextLocked(Candidator, term) {
		LOG(rf.me, rf.currentTerm, DVote, "Lost Candidator to %s, abort RequestVote", rf.role)
		return false
	}

	for peer := 0; peer < len(rf.peers); peer++ {
		if peer == rf.me {
			votes++
			continue
		}

		args := &RequestVoteArgs{
			Term:       term,
			Candidator: rf.me,
		}
		go askVoteFromPeer(peer, args)
	}
	return true
}

func (rf *Raft) CheckContextLocked(role Role, term int) bool {
	return !(rf.role == role && rf.currentTerm == term)
}

func (rf *Raft) replicationTicker(term int) {
	for !rf.killed() {
		ok := rf.startReplication(term)
		if !ok {
			break
		}
		time.Sleep(HeartbeatInterval)
	}
}

type AppendEntriesArgs struct {
	Term     int
	LeaderId int
}

type AppendEntriesReply struct {
	Term    int
	Success bool
}

// 同步 wal
func (rf *Raft) startReplication(term int) bool {
	replicationToPeer := func(peer int, args *AppendEntriesArgs) {
		reply := &AppendEntriesReply{}
		ok := rf.sendAppendEntries(peer, args, reply)

		rf.mu.Lock()
		defer rf.mu.Unlock()

		if !ok {
			LOG(rf.me, rf.currentTerm, DLog, "appendEntries -> S%d Failed, Lost Or error", peer)
			return
		}

		if reply.Term > rf.currentTerm {
			rf.becomeFollowerLocked(reply.Term)
			return
		}
	}

	rf.mu.Lock()
	defer rf.mu.Unlock()
	if rf.CheckContextLocked(Leader, term) {
		LOG(rf.me, rf.currentTerm, DLog, "Lost Leader [T%d] to %s[T%d]", term, rf.role, rf.currentTerm)
		return false
	}

	for peer := 0; peer < len(rf.peers); peer++ {
		if peer == rf.me {
			continue
		}

		args := &AppendEntriesArgs{
			Term:     term,
			LeaderId: rf.me,
		}

		go replicationToPeer(peer, args)
	}
	return true
}

func (rf *Raft) sendAppendEntries(peer int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	ok := rf.peers[peer].Call("Raft.AppendEntries", args, reply)
	return ok
}

// 服务或测试程序想要创建一个Raft服务器。所有Raft服务器（包括本服务器）的端口都在peers[]数组中。
// 本服务器的端口是peers[me]。所有服务器的peers[]数组顺序相同。
// persist是这个服务器用来保存其持久状态的地方，并且最初保存着最近保存的状态（如果有的话）。
// applyCh是一个通道，测试程序或服务期望Raft将ApplyMsg消息发送到该通道上。
// Make()必须快速返回，因此它应该为任何长时间运行的工作启动goroutines。
func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me

	// Your initialization code here (PartA, PartB, PartC).
	rf.currentTerm = 0
	rf.role = Follower
	rf.votedFor = -1

	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())

	// start ticker goroutine to start elections
	go rf.electionTicker()

	return rf
}
