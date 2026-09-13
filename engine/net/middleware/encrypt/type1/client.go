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
		key2, publicKeyBytes, err := m.getKeyAndStrByBytes(data)
		if err != nil {
			m.status = ClientStatusError
			m.mu.Unlock()
			return err
		}
		m.Key2 = key2
		publicKey, err := m.GetPublicKeyByBytes(publicKeyBytes)
		if err != nil {
			m.status = ClientStatusError
			m.mu.Unlock()
			return err
		}
		csNo, err := m.GenKey()
		if err != nil {
			m.status = ClientStatusError
			m.mu.Unlock()
			return err
		}
		m.CsNo = csNo
		csNoBytes := m.GetKeyBytes(m.CsNo, keySize)
		// 必须先置状态再发送：对端应答可能在本调用返回前到达接收 goroutine
		m.status = ClientStatusWaitScNo
		m.mu.Unlock()

		sendData, err := m.EncryptWithPublicKey(csNoBytes, publicKey)
		if err != nil {
			// 修复 L-2：此前只 return err 而不置 Error 状态，状态机会滞留在
			// WaitScNo；与其它错误路径保持一致。
			m.mu.Lock()
			m.status = ClientStatusError
			m.mu.Unlock()
			return err
		}
		return m.Pre().SendData(sendData)

	case ClientStatusWaitScNo:
		// 修复 M-06：本分支此前**完全不校验长度**（server 侧同类分支都有检查）。
		// 短帧会被 GetKeyByBytes 静默补零成"高位全零的 ScNo"并直接置
		// HandsFinish，于是双方序号永久错开，且攻击者可控字节进入掩码。
		m.Key1Key2CsNoDecrypt(data)
		if len(data) != keySize {
			m.status = ClientStatusError
			m.mu.Unlock()
			return fmt.Errorf("ClientStatusWaitScNo data len(%d) != %d", len(data), keySize)
		}
		scNo, err := m.GetKeyByBytes(data, keySize)
		if err != nil {
			m.status = ClientStatusError
			m.mu.Unlock()
			return err
		}
		m.ScNo = scNo
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
			// 修复 L-1：原先在 Unlock() **之后**才读 m.status 拼诊断信息，
			// 既是数据竞争，又必然读到刚写入的 ClientStatusError（诊断恒为 5）。
			old := m.status
			m.status = ClientStatusError
			m.mu.Unlock()
			panic(fmt.Sprintf("type1ClientNetEncrypt.OnEvent OnConnect state error: %v", old))
		}
		key1, err := m.GenKey()
		if err != nil {
			m.status = ClientStatusError
			m.mu.Unlock()
			// 与相邻的状态错误分支保持同一形态。注意 Key=0 意味着掩码全零，
			// 绝不能"降级继续握手"——宁可失败也不能明文上网。
			panic(fmt.Sprintf("type1ClientNetEncrypt.OnEvent cannot generate key: %v", err))
		}
		m.Key1 = key1
		data := m.GetKeyBytes(m.Key1, keySize)
		// 必须先置状态再发送：对端应答可能在本调用返回前就到达接收 goroutine
		// （握手期竞争的根因）
		m.status = ClientStatusWaitKey2AndPK
		m.mu.Unlock()
		// 加密key1
		m.TableEncrypt(data)
		// 发送key1
		if err := m.Pre().SendData(data); err != nil {
			m.mu.Lock()
			m.status = ClientStatusError
			m.mu.Unlock()
			return
		}
	}
}
