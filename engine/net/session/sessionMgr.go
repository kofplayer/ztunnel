package netSession

import (
	"sync"
)

func NewSessionMgr() SessionMgr {
	v := new(sessionMgr)
	v.sessions = make(map[SessionID]*netSession)
	return v
}

type SessionMgr interface {
	NewSession() NetSession
	RemoveSession(sID SessionID)
	GetSession(sID SessionID) NetSession
	TravelSession(f func(s NetSession) bool)
}

type sessionMgr struct {
	lock     sync.RWMutex
	sessions map[SessionID]*netSession
	genUId   SessionID
}

func (sm *sessionMgr) NewSession() NetSession {
	v := new(netSession)
	v.Init()
	sm.lock.Lock()
	defer sm.lock.Unlock()
	// ID 自增必须在锁内：锁外自增在并发创建时会产生重复会话 ID
	sm.genUId++
	v.id = sm.genUId
	sm.sessions[v.id] = v
	return v
}

func (sm *sessionMgr) RemoveSession(sID SessionID) {
	sm.lock.Lock()
	defer sm.lock.Unlock()
	delete(sm.sessions, sID)
}

func (sm *sessionMgr) GetSession(sID SessionID) NetSession {
	sm.lock.RLock()
	defer sm.lock.RUnlock()
	v, ok := sm.sessions[sID]
	if !ok {
		return nil
	}
	return v
}

func (sm *sessionMgr) TravelSession(f func(s NetSession) bool) {
	sm.lock.RLock()
	defer sm.lock.RUnlock()
	for _, v := range sm.sessions {
		if !f(v) {
			break
		}
	}
}
