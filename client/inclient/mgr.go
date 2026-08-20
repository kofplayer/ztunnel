package inclient

import (
	"sync"

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

func (m *ClientMgr) OpenClient(connectId netSession.SessionID, host string, port uint16, outcli client.NetClient) (client.NetClient, error) {
	cli := NewClient(connectId, host, port, outcli, m)
	err := cli.Connect()
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clients[connectId] = cli
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
	cli.Disconnect()
}

// RemoveClient 连接已断开时仅清理 map 条目，不调用 Disconnect
func (m *ClientMgr) RemoveClient(connectId netSession.SessionID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.clients, connectId)
}

func (m *ClientMgr) CloseAllClient() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, cli := range m.clients {
		cli.Disconnect()
	}
	clear(m.clients)
}
