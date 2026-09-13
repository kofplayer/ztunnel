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
	Len() int
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
	// （报告 #17）。
	//
	// 另外必须**探测空闲槽位**：genUId 是 uint32，回绕后直接
	// `sessions[id] = v` 会无条件顶掉一个仍在使用的会话——老会话从此再也
	// 拿不到自己的 map 条目，RemoveSession 也命中不了它，于是永久失联并泄漏
	// （报告 M-03；inclient 侧同 id 覆盖会造成跨连接串流，那边已加身份核对）。
	//
	// 循环可证明终止：只要 map 中会话数 < 2^32（受内存约束必然成立），
	// 最坏也只是穿过一段连续的已占用 ID 区间。
	for {
		sm.genUId++
		if _, taken := sm.sessions[sm.genUId]; !taken {
			break
		}
	}
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
	// 锁内只做快照，回调一律在锁外执行。
	//
	// 回调链可能反过来拿这把锁的**写锁**：Stop() 的回调是 s.Close()，而断开
	// 通知现在会派发到 netServer 的 RemoveSession。RWMutex 在有 writer 排队时
	// 会阻塞后续 RLock，同 goroutine 内持 RLock 调 RemoveSession 即自死锁
	// （报告 M-11）。持 RLock 跑回调还会让期间所有断连清理被串行阻塞。
	sm.lock.RLock()
	snapshot := make([]NetSession, 0, len(sm.sessions))
	for _, v := range sm.sessions {
		snapshot = append(snapshot, v)
	}
	sm.lock.RUnlock()

	for _, s := range snapshot {
		if !f(s) {
			return
		}
	}
}

// Len 返回当前会话数。供运行时监控与测试断言"会话已被回收"使用——
// 没有这个接口，map 泄漏类问题在测试里几乎无法断言（报告 E-09）。
func (sm *sessionMgr) Len() int {
	sm.lock.RLock()
	defer sm.lock.RUnlock()
	return len(sm.sessions)
}
