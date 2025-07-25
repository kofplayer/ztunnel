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
	sm.genUId++
	v.id = sm.genUId
	sm.lock.Lock()
	defer sm.lock.Unlock()
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
