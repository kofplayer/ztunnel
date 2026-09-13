package netSession

import "testing"

// 本文件是**包内**测试：需要直接操纵 sessionMgr 的未导出字段 genUId 才能模拟
// uint32 回绕。也因此不能引用 testutil —— testutil 依赖本包，会造成 import cycle。

// 回归 M-03：genUId 是 uint32，回绕后原先的 `sessions[id] = v` 会**无条件顶掉**
// 一个仍在使用的会话——老会话再也拿不到自己的 map 条目，RemoveSession 也命中
// 不了它，于是永久失联并泄漏（跨隧道时还会造成 connectId 串流）。
//
// 这里把计数器直接推到回绕点，并预先占住回绕后的头几个 ID，验证新实现会
// **线性探测空闲槽位**而不是覆盖。
func TestSessionMgr_IDWraparound_DoesNotEvictLiveSession(t *testing.T) {
	sm := NewSessionMgr().(*sessionMgr)

	// 预先占住 ID 0 与 1（回绕后会先落到这两个值上）
	ghost0 := new(netSession)
	ghost0.id = 0
	sm.sessions[0] = ghost0
	ghost1 := new(netSession)
	ghost1.id = 1
	sm.sessions[1] = ghost1

	sm.genUId = SessionID(0xFFFFFFFF - 1)

	a := sm.NewSession() // 0xFFFFFFFF 空闲，直接取
	if a.GetID() != SessionID(0xFFFFFFFF) {
		t.Fatalf("第一次分配应拿到 0xFFFFFFFF, got %d", a.GetID())
	}

	// 回绕到 0（被占）→ 探测 1（被占）→ 2（空闲）
	b := sm.NewSession()
	if b.GetID() != SessionID(2) {
		t.Fatalf("M-03 未修复：回绕后应探测空闲槽位，而不是覆盖被占用的 ID, got %d", b.GetID())
	}

	// 关键：被占用的老会话必须还活着且可取回
	if sm.GetSession(0) != ghost0 {
		t.Fatal("M-03 未修复：ID 0 上的会话被静默顶掉了")
	}
	if sm.GetSession(1) != ghost1 {
		t.Fatal("M-03 未修复：ID 1 上的会话被静默顶掉了")
	}
	if sm.GetSession(SessionID(0xFFFFFFFF)) != a {
		t.Fatal("新会话 a 应能从 map 取回")
	}
	if sm.Len() != 4 {
		t.Fatalf("map 里应有 4 个互不覆盖的条目, got %d", sm.Len())
	}
}

// 常态路径：连续分配必须互不重复，且逐个移除都能真正摘掉（没有互相覆盖过）。
func TestSessionMgr_ConsecutiveIDs_AreUnique(t *testing.T) {
	sm := NewSessionMgr()
	seen := make(map[SessionID]bool, 100)
	ids := make([]SessionID, 0, 100)
	for i := 0; i < 100; i++ {
		s := sm.NewSession()
		if seen[s.GetID()] {
			t.Fatalf("出现重复会话 ID %d", s.GetID())
		}
		seen[s.GetID()] = true
		ids = append(ids, s.GetID())
	}
	for _, id := range ids {
		sm.RemoveSession(id)
	}
	if sm.Len() != 0 {
		t.Fatalf("全部移除后应为空, Len=%d（说明有 ID 被覆盖导致移除落空）", sm.Len())
	}
}
