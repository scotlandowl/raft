package raft

import (
	"sort"
	"time"
)

type LogEntry struct {
	CommandValid bool        // 是否需要被 apply 到状态机
	Command      interface{} // 需要被 apply 到状态机的 command
	Term         int
}

type AppendEntriesArgs struct {
	// index 和 term 确认唯一 wal
	Term     int
	LeaderId int
	// 待同步 log 的前一条 log 的 term 和 index
	PrevLogIndex int
	PrevLogTerm  int
	// 表示待附加到 Follower 节点日志中的日志条目
	Entries []LogEntry
	// Leader 的 commit 索引，用于让 Follower 节点更新自己的 commit 索引。
	LeaderCommit int
}

type AppendEntriesReply struct {
	Term    int
	Success bool
}

const replicationInterval time.Duration = 200 * time.Millisecond

// 状态
func (rf *Raft) becomeFollowerLocked(term int) {
	// 对方 term 小于己方 term，拒绝
	if term < rf.currentTerm {
		LOG(rf.me, rf.currentTerm, DError, "Can't become follower, lower term: T%d", term)
		return
	}
	LOG(rf.me, rf.currentTerm, DLog, "%s -> Follower at term: T%v->T%v", rf.role, rf.currentTerm, term)
	rf.role = Follower
	// 投票
	if term > rf.currentTerm {
		rf.votedFor = -1
	}
	rf.currentTerm = term
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

// 返回当前任期号以及该节点是否认为自己是 Leader
func (rf *Raft) GetState() (int, bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.currentTerm, rf.role == Leader
}

func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	LOG(rf.me, rf.currentTerm, DDebug, "<- S%d, Receive Log, Prev=[%d]T%d, Len()=%d", args.LeaderId, args.PrevLogIndex, args.PrevLogTerm, len(args.Entries))

	reply.Term = rf.currentTerm
	reply.Success = false

	// 当前节点 term 更大
	if args.Term < rf.currentTerm {
		LOG(rf.me, rf.currentTerm, DLog2, "<- S%d, Reject, Higher term, T%d <- T%d", args.LeaderId, args.Term, rf.currentTerm)
		return
	}
	if args.Term >= rf.currentTerm {
		rf.becomeFollowerLocked(args.Term)
	}

	// 如果当前节点 的 wal index >= 对方的 prevLogIndex，说明有日志缺失
	if args.PrevLogIndex >= len(rf.log) {
		LOG(rf.me, rf.currentTerm, DLog2, "<- S%d, Reject Log, Follower log too short, Len: %d <= Prev:%d", args.LeaderId, len(rf.log), args.PrevLogIndex)
		return
	}

	// term 不匹配
	if rf.log[args.PrevLogIndex].Term != args.PrevLogTerm {
		LOG(rf.me, rf.currentTerm, DLog2, "<- S%d, Reject Log, Prev log not match, [%d]: T%d != T%d", args.LeaderId, args.PrevLogIndex, rf.log[args.PrevLogIndex].Term, args.PrevLogTerm)
		return
	}

	// prevLogIndex 匹配成功，同步日志
	rf.log = append(rf.log[:args.PrevLogIndex+1], args.Entries...)
	reply.Success = true
	LOG(rf.me, rf.currentTerm, DLog2, "Follower append logs: (%d, %d]", args.PrevLogIndex, args.PrevLogIndex+len(args.Entries))

	// 处理提交日志
	if args.LeaderCommit > rf.commitIndex {
		LOG(rf.me, rf.currentTerm, DApply, "Follower update the commit index %d->%d", rf.commitIndex, args.LeaderCommit)
		rf.commitIndex = args.LeaderCommit
		if rf.commitIndex >= len(rf.log) {
			rf.commitIndex = len(rf.log) - 1
		}
		// 触发提交
		rf.applyCond.Signal()
	}

	rf.resetElectionTimerLocked()
}

func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	ok := rf.peers[server].Call("Raft.AppendEntries", args, reply)
	return ok
}

func (rf *Raft) replicationTicker(term int) {
	for !rf.killed() {
		ok := rf.startReplication(term)
		if !ok {
			break
		}
		time.Sleep(replicationInterval)
	}
}

func (rf *Raft) getMajorityIndexLocked() int {
	tmpIndexes := make([]int, len(rf.peers))
	copy(tmpIndexes, rf.matchIndex)
	sort.Ints(sort.IntSlice(tmpIndexes))
	majorityIdx := (len(rf.peers) - 1) / 2
	LOG(rf.me, rf.currentTerm, DDebug, "Match index after sort: %v, majority[%d]=%d", tmpIndexes, majorityIdx, tmpIndexes[majorityIdx])
	return tmpIndexes[majorityIdx]
}

func (rf *Raft) startReplication(term int) bool {
	replicationToPeer := func(peer int, args *AppendEntriesArgs) {
		reply := &AppendEntriesReply{}
		ok := rf.sendAppendEntries(peer, args, reply)

		rf.mu.Lock()
		defer rf.mu.Unlock()
		if !ok {
			LOG(rf.me, rf.currentTerm, DLog, "-> S%d, Lost or crashed", peer)
			return
		}

		// 如果 请求同步的响应 Term 大于 自己的，退位
		if reply.Term > rf.currentTerm {
			rf.becomeFollowerLocked(reply.Term)
			return
		}

		// 确认当前节点的 身份 和 Term
		if rf.contextLostLocked(Leader, term) {
			LOG(rf.me, rf.currentTerm, DLog, "Context Lost, T%d:Leader->T%d:%s", term, rf.currentTerm, rf.role)
			return
		}

		// 如果 prevLog 匹配失败，则探查 更往前的 log index
		if !reply.Success {
			idx, term := args.PrevLogIndex, args.PrevLogTerm
			for idx > 0 && rf.log[idx].Term == term {
				idx--
			}
			rf.nextIndex[peer] = idx + 1
			LOG(rf.me, rf.currentTerm, DLog, "-> S%d, Not matched at %d, try next=%d", peer, args.PrevLogIndex, rf.nextIndex[peer])
			// 重置选举计时，因为 Leader 有可能转变为 Follower
			rf.resetElectionTimerLocked()
			return
		}

		// 如果匹配成功，更新 commiteIndex 和每个 peer 的下一个 index
		rf.matchIndex[peer] = args.PrevLogIndex + len(args.Entries)
		rf.nextIndex[peer] = rf.matchIndex[peer] + 1

		// 更新 commitIndex
		majorityMatched := rf.getMajorityIndexLocked()
		if majorityMatched > rf.commitIndex {
			LOG(rf.me, rf.currentTerm, DApply, "Leader update the commit index %d->%d", rf.commitIndex, args.LeaderCommit)
			rf.commitIndex = majorityMatched
			rf.applyCond.Signal()
		}
	}

	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rf.contextLostLocked(Leader, term) {
		LOG(rf.me, rf.currentTerm, DLog, "Lost Leader [%d] to %s[T%d]", term, rf.role, rf.currentTerm)
		return false
	}

	for peer := 0; peer < len(rf.peers); peer++ {
		if peer == rf.me {
			rf.matchIndex[peer] = len(rf.log) - 1
			rf.nextIndex[peer] = len(rf.log)
			continue
		}

		// nextIndex 是要发送给对等节点的下一个日志条目的索引
		// matchIndex 是已知已在对等节点上复制的最高日志条目的索引
		// 1. 当成为领导者时，nextIndex 初始化为领导者最后日志索引 + 1（成为领导者时初始化 nextIndex）
		// 2. matchIndex 初始化为 0
		// 3. 向对等节点的复制将从 nextIndex - 1 开始
		prevIdx := rf.nextIndex[peer] - 1
		prevTerm := rf.log[prevIdx].Term
		args := &AppendEntriesArgs{
			Term:         rf.currentTerm,
			LeaderId:     rf.me,
			PrevLogIndex: prevIdx,
			PrevLogTerm:  prevTerm,
			Entries:      rf.log[prevIdx+1:],
			LeaderCommit: rf.commitIndex, // 告知 follower commitIndex
		}

		go replicationToPeer(peer, args)
	}
	return true
}
