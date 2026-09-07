package type1NetEncrypt

import (
	"fmt"

	netMiddleware "ztunnel/engine/net/middleware"
)

func NewClientNetEncrypt() netMiddleware.Middleware {
	return &ClientNetEncrypt{
		status: ClientStatusWaitConnected,
	}
}

type ClientStatus int32

const (
	ClientStatusWaitConnected ClientStatus = 0
	ClientStatusWaitKey2AndPK ClientStatus = 15
	ClientStatusWaitScNo      ClientStatus = 2
	ClientStatusHandsFinish   ClientStatus = 4
	ClientStatusError         ClientStatus = 5
)

type ClientNetEncrypt struct {
	BaseNetEncrypt
	status ClientStatus
}

func (m *ClientNetEncrypt) ReceiveData(data []byte) error {
	// OnEvent（拨号 goroutine）与本方法（接收 goroutine）可能并发执行，
	// 握手状态机必须互斥（-race 实测竞争点）。锁内只做状态与密钥变更，
	// 阻塞发送与下游分发放锁外：onReady 回调可能同步回发消息，
	// 持锁跨过 SendData/FireEvent 会造成重入死锁。
	m.mu.Lock()
	switch m.status {
	case ClientStatusWaitKey2AndPK:
		m.Key1Decrypt(data)
		key2, publicKeyBytes := m.getKeyAndStrByBytes(data)
		m.Key2 = key2
		publicKey, err := m.GetPublicKeyByBytes(publicKeyBytes)
		if err != nil {
			m.status = ClientStatusError
			m.mu.Unlock()
			return err
		}
		m.CsNo = m.GenKey()
		csNoBytes := m.GetKeyBytes(m.CsNo, keySize)
		m.status = ClientStatusWaitScNo
		m.mu.Unlock()

		sendData, err := m.EncryptWithPublicKey(csNoBytes, publicKey)
		if err != nil {
			return err
		}
		return m.Pre().SendData(sendData)
	case ClientStatusWaitScNo:
		m.Key1Key2CsNoDecrypt(data)
		m.ScNo = m.GetKeyByBytes(data, keySize)
		m.status = ClientStatusHandsFinish
		m.mu.Unlock()
		m.FireEvent(netMiddleware.MiddlewareEventOnReady)
		return nil
	case ClientStatusHandsFinish:
		m.mu.Unlock()
		m.GoNextScNo()
		m.Key1Key2ScNoDecrypt(data)
		return m.Next().ReceiveData(data)
	default:
		status := m.status
		m.mu.Unlock()
		return fmt.Errorf("status %v error", status)
	}
}

func (m *ClientNetEncrypt) SendData(bytes []byte) error {
	m.mu.Lock()
	status := m.status
	m.mu.Unlock()
	if status != ClientStatusHandsFinish {
		return fmt.Errorf("status %v error", status)
	}
	m.sendMu.Lock()
	defer m.sendMu.Unlock()
	m.GoNextCsNo()
	m.Key1Key2CsNoEncrypt(bytes)
	return m.Pre().SendData(bytes)
}

func (m *ClientNetEncrypt) OnEvent(e netMiddleware.MiddlewareEvent) {
	m.BaseNetEncrypt.OnEvent(e)
	switch e {
	case netMiddleware.MiddlewareEventOnConnect:
		m.mu.Lock()
		if m.status != ClientStatusWaitConnected {
			m.status = ClientStatusError
			m.mu.Unlock()
			panic(fmt.Sprintf("type1ClientNetEncrypt.OnEvent OnConnect state error: %v", m.status))
		}
		// 生成key1
		m.Key1 = m.GenKey()
		data := m.GetKeyBytes(m.Key1, keySize)
		// 必须先置状态再发送：Pre().SendData 是阻塞调用，对端应答可能在
		// 本调用返回前就到达接收 goroutine（握手期竞争的根因）
		m.status = ClientStatusWaitKey2AndPK
		m.mu.Unlock()
		// 加密key1
		m.TableEncrypt(data)
		// 发送key1
		m.Pre().SendData(data)
	}
}
