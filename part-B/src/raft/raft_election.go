package raft

import (
	"math/rand"
	"time"
)

const (
	tickTime           = 100 * time.Millisecond
	electionTimeoutMin = 5 * tickTime
	electionTimeoutMax = 10 * tickTime
)

// 重置 选举过期时间
func (rf *Raft) resetElectionTimerLocked() {
	rf.electionStart = time.Now()
	randRange := int64(electionTimeoutMax - electionTimeoutMin)
	rf.electionTimeout = time.Duration(rand.Int63()%randRange) + electionTimeoutMin
}

// 判断是否到达 选举时间
func (rf *Raft) isElectionTimeoutLocked() bool {
	return time.Since(rf.electionStart) > rf.electionTimeout
}

// 判断 自己的 最新的 log 是否比 candidate 的最新的 log 更新
func (rf *Raft) isMoreUpToDate(candidateIndex, candidateTerm int) bool {
	l := len(rf.log)
	lastIndex, lastTerm := l-1, rf.log[l-1].Term

	LOG(rf.me, rf.currentTerm, DVote, "Compare last log, Me: [%d]T%d, Candidate: [%d]T%d", lastIndex, lastTerm, candidateIndex, candidateTerm)
	if lastTerm != candidateTerm {
		return lastTerm > candidateTerm
	}
	return lastIndex > candidateIndex
}

// candidate 的请求投票参数
type RequestVoteArgs struct {
	Term         int
	CandidateId  int
	LastLogIndex int
	LastLogTerm  int
}

// candidate 请求投票的响应
type RequestVoteReply struct {
	Term        int
	VoteGranted bool
}

func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	reply.Term = rf.currentTerm
	reply.VoteGranted = false

	// 如果 candidate 的 Term 小于 自己的
	if args.Term < rf.currentTerm {
		LOG(rf.me, rf.currentTerm, DVote, "-> S%d, Reject voted, higher term, T%d>T%d", args.CandidateId, rf.currentTerm, args.Term)
		reply.VoteGranted = false
		return
	}

	// 如果 candidate 的 Term 大于 自己的
	if args.Term > rf.currentTerm {
		rf.becomeFollowerLocked(args.Term)
	}

	// 判断 自己 是否投过票
	if rf.votedFor != -1 {
		LOG(rf.me, rf.currentTerm, DVote, "-> S%d, Reject voted, Already voted to S%d", args.CandidateId, rf.votedFor)
		return
	}

	// 如果 candidate 的最新 log 的 Index 小于 自己的
	if rf.isMoreUpToDate(args.LastLogIndex, args.LastLogIndex) {
		LOG(rf.me, rf.currentTerm, DVote, "-> S%d, Reject voted, Candidate's log is less up to date", args.CandidateId)
		return
	}

	reply.VoteGranted = true
	rf.votedFor = args.CandidateId
	rf.resetElectionTimerLocked()
	LOG(rf.me, rf.currentTerm, DVote, "-> S%d, Vote granted", args.CandidateId)
}

func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}

// candidator 发起选举
func (rf *Raft) startElection(term int) bool {
	votes := 0
	askVoteFromPeer := func(peer int, args *RequestVoteArgs) {
		reply := &RequestVoteReply{}
		ok := rf.sendRequestVote(peer, args, reply)

		rf.mu.Lock()
		defer rf.mu.Unlock()

		// 请求失败或丢失
		if !ok {
			LOG(rf.me, rf.currentTerm, DDebug, "Ask vote from S%d, Lost or error", peer)
			return
		}

		// 如果 请求响应 的 Term 较大
		if reply.Term > rf.currentTerm {
			rf.becomeFollowerLocked(reply.Term)
			return
		}

		// 确认 本节点的 身份 和 Term, 如果不符合
		if rf.contextLostLocked(Candidator, term) {
			LOG(rf.me, rf.currentTerm, DVote, "Lost context, abort RequestVoteReply from S%d", peer)
			return
		}

		// 计票
		if reply.VoteGranted {
			votes++
			if votes > len(rf.peers)/2 {
				rf.becomeLeaderLocked()
				go rf.replicationTicker(term)
			}
		}
	}

	rf.mu.Lock()
	defer rf.mu.Unlock()
	if rf.contextLostLocked(Candidator, term) {
		LOG(rf.me, rf.currentTerm, DVote, "Lost Candidator to %s, abort RequesetVote", rf.role)
		return false
	}

	l := len(rf.log)
	for peer := 0; peer < len(rf.peers); peer++ {
		// 直接给自己投票
		if peer == rf.me {
			votes++
			continue
		}
		args := &RequestVoteArgs{
			Term:         term,
			CandidateId:  rf.me,
			LastLogIndex: l - 1,
			LastLogTerm:  rf.log[l-1].Term,
		}
		go askVoteFromPeer(peer, args)
	}

	return true
}

func (rf *Raft) electionTicker() {
	for !rf.killed() {
		rf.mu.Lock()
		if rf.role != Leader && rf.isElectionTimeoutLocked() {
			rf.becomeCandidateLocked()
			go rf.startElection(rf.currentTerm)
		}
		rf.mu.Unlock()

		ms := 50 + (rand.Int63() % 300)
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
}
