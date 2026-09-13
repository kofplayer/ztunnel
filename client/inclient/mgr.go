package inclient

import (
	"sync"

	"ztunnel/engine/log"
	"ztunnel/engine/net/client"
	netSession "ztunnel/engine/net/session"
)

func NewClientMgr() *ClientMgr {
	return &ClientMgr{
		clients: make(map[netSession.SessionID]client.NetClient),
	}
}

type ClientMgr struct {
	mu      sync.Mutex
	clients map[netSession.SessionID]client.NetClient
}

// 持锁纪律（重要）：Disconnect() 会在调用 goroutine 内**同步**派发 OnDisconnect，
// 而 inclient.handler.OnDisconnect 会回调 RemoveClient() —— 那要拿同一把 mu。
// 因此**任何**持 m.mu 的路径都不得调用 Disconnect()：一律先快照、解锁，再断连。
// （SEC-01 修复之前断开通知在主动关闭路径上被守卫吞掉，这条重入路径当时不可达。）

// OpenClient 建立并登记一个到内网真实服务的转发客户端。
//
// 修复 M-15：**先登记后拨号**。此前是 Connect() 成功之后才写 map，而 Connect()
// 返回时该连接的 receiver goroutine 已经在跑——若内网服务 accept 后立即关闭
// （MySQL 连接数拒绝、LB 健康检查、`nc -z` 探测都是常态），
// OnDisconnect → RemoveClient 会先删掉一个**还不存在**的条目，随后死客户端
// 被写回 map 并永久驻留：该 connectId 的用户数据从此静默丢弃。
//
// 修复 M-03 的一部分：同 id 已存在时必须先关掉老的。会话 ID 是 uint32 自增，
// 回绕后会撞上仍在用的 id，而 inclient 的 handler 用的是**闭包里的 connectId**，
// 不顶掉老连接就会把老用户在内网服务侧的响应打上同一个 id 投递给新用户
// （跨连接串流）。
func (m *ClientMgr) OpenClient(connectId netSession.SessionID, host string, port uint16, outcli client.NetClient) (client.NetClient, error) {
	cli := NewClient(connectId, host, port, outcli, m)

	m.mu.Lock()
	old := m.clients[connectId]
	m.clients[connectId] = cli
	m.mu.Unlock()

	if old != nil {
		// 必须先登记新条目再关老连接：老连接的 OnDisconnect 会带着自己的身份
		// 调 RemoveClient，身份核对保证它删不掉刚登记的新条目。
		_ = old.Disconnect()
	}

	if err := cli.Connect(); err != nil {
		m.mu.Lock()
		if m.clients[connectId] == cli {
			delete(m.clients, connectId)
		}
		m.mu.Unlock()
		return nil, err
	}
	return cli, nil
}

func (m *ClientMgr) GetClient(connectId netSession.SessionID) client.NetClient {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.clients[connectId]
}

func (m *ClientMgr) CloseClient(connectId netSession.SessionID) {
	m.mu.Lock()
	cli := m.clients[connectId]
	delete(m.clients, connectId)
	m.mu.Unlock()
	if cli == nil {
		return
	}
	_ = cli.Disconnect()
}

// RemoveClient 在连接已自行断开时仅清理 map 条目，不再调 Disconnect。
//
// expect 用于**身份核对**：只有当前登记的确实就是发起删除的那个客户端时才删。
// 缺这层核对时，替换同 id 的老连接会被老连接的回调误删掉刚登记的新条目。
func (m *ClientMgr) RemoveClient(connectId netSession.SessionID, expect client.NetClient) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.clients[connectId]
	if !ok {
		return
	}
	if cur == expect {
		delete(m.clients, connectId)
		return
	}
	// 槽位已被新连接接管：正常现象，留条日志便于排障，不动别人的条目。
	if l := log.Main(); l != nil {
		l.Debug("RemoveClient %v skipped: slot already taken by a newer client", connectId)
	}
}

// CloseAllClient 关闭全部转发客户端。
//
// 锁外 Disconnect：持 m.mu 调用 Disconnect 会经 OnDisconnect 回调重入
// RemoveClient（同一把 mu）造成**自死锁**——SEC-01 修复后这条重入路径才真正可达。
func (m *ClientMgr) CloseAllClient() {
	m.mu.Lock()
	clients := make([]client.NetClient, 0, len(m.clients))
	for id, cli := range m.clients {
		clients = append(clients, cli)
		delete(m.clients, id)
	}
	m.mu.Unlock()

	for _, cli := range clients {
		_ = cli.Disconnect()
	}
}

// Len 返回当前登记的转发客户端数，供测试与运行时监控断言是否泄漏。
func (m *ClientMgr) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.clients)
}
