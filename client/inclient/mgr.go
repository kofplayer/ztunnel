package inclient

import (
	"ztunnel/engine/net/client"
	netSession "ztunnel/engine/net/session"
)

func NewClientMgr() *ClientMgr {
	return &ClientMgr{
		clients: make(map[netSession.SessionID]client.NetClient),
	}
}

type ClientMgr struct {
	clients map[netSession.SessionID]client.NetClient
}

func (m *ClientMgr) OpenClient(connectId netSession.SessionID, host string, port uint16, outcli client.NetClient) (client.NetClient, error) {
	cli := NewClient(connectId, host, port, outcli)
	m.clients[connectId] = cli
	return cli, cli.Connect()
}

func (m *ClientMgr) GetClient(connectId netSession.SessionID) client.NetClient {
	return m.clients[connectId]
}

func (m *ClientMgr) CloseClient(connectId netSession.SessionID) {
	cli := m.clients[connectId]
	if cli == nil {
		return
	}
	cli.Disconnect()
	delete(m.clients, connectId)
}

func (m *ClientMgr) CloseAllClient() {
	for _, cli := range m.clients {
		cli.Disconnect()
	}
	clear(m.clients)
}
