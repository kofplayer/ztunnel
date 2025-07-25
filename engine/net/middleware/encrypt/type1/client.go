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
	switch m.status {
	case ClientStatusWaitKey2AndPK:
		m.Key1Decrypt(data)
		key2, publicKeyBytes := m.getKeyAndStrByBytes(data)
		m.Key2 = key2
		publicKey, err := m.GetPublicKeyByBytes(publicKeyBytes)
		if err != nil {
			m.status = ClientStatusError
			return err
		}
		m.CsNo = m.GenKey()
		csNoBytes := m.GetKeyBytes(m.CsNo, keySize)
		sendData, err := m.EncryptWithPublicKey(csNoBytes, publicKey)
		if err != nil {
			m.status = ClientStatusError
			return err
		}
		m.Pre().SendData(sendData)
		m.status = ClientStatusWaitScNo
		return nil
	case ClientStatusWaitScNo:
		m.Key1Key2CsNoDecrypt(data)
		m.ScNo = m.GetKeyByBytes(data, keySize)
		m.status = ClientStatusHandsFinish
		m.FireEvent(netMiddleware.MiddlewareEventOnReady)
		return nil
	case ClientStatusHandsFinish:
		m.GoNextScNo()
		m.Key1Key2ScNoDecrypt(data)
		return m.Next().ReceiveData(data)
	default:
		return fmt.Errorf("status %v error", m.status)
	}
}

func (m *ClientNetEncrypt) SendData(bytes []byte) error {
	if m.status != ClientStatusHandsFinish {
		return fmt.Errorf("status %v error", m.status)
	}
	m.GoNextCsNo()
	m.Key1Key2CsNoEncrypt(bytes)
	return m.Pre().SendData(bytes)
}

func (m *ClientNetEncrypt) OnEvent(e netMiddleware.MiddlewareEvent) {
	m.BaseNetEncrypt.OnEvent(e)
	switch e {
	case netMiddleware.MiddlewareEventOnConnect:
		if m.status != ClientStatusWaitConnected {
			m.status = ClientStatusError
			panic(fmt.Sprintf("type1ClientNetEncrypt.OnEvent OnConnect state error: %v", m.status))
			return
		}
		// 生成key1
		m.Key1 = m.GenKey()
		// 加密key1
		data := m.GetKeyBytes(m.Key1, keySize)
		m.TableEncrypt(data)
		// 发送key1
		m.Pre().SendData(data)
		m.status = ClientStatusWaitKey2AndPK
	}
}
