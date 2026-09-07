package type1NetEncrypt

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"fmt"

	netMiddleware "ztunnel/engine/net/middleware"
)

func genRSA(bits int) (*rsa.PrivateKey, []byte, error) {
	// 生成密钥对
	privateKey, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return nil, nil, err
	}

	// 将公钥序列化为PKIX格式
	publicKeyBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return nil, nil, err
	}
	return privateKey, publicKeyBytes, nil
}

func CreateServerNetEncryptFunc() netMiddleware.CreateMiddlewareFunc {
	privateKey, publicKeyBytes, err := genRSA(1024)
	if err != nil {
		return nil
	}
	return func() netMiddleware.Middleware {
		return &ServerNetEncrypt{
			publicKeyBytes: publicKeyBytes,
			privateKey:     privateKey,
			status:         ServerStatusWaitKey1,
		}
	}
}

type ServerStatus int32

const (
	ServerStatusWaitKey1    ServerStatus = 0
	ServerStatusWaitCsNo    ServerStatus = 1
	ServerStatusHandsFinish ServerStatus = 2
	ServerStatusError       ServerStatus = 4
)

type ServerNetEncrypt struct {
	BaseNetEncrypt
	publicKeyBytes []byte
	privateKey     *rsa.PrivateKey
	status         ServerStatus
}

func (m *ServerNetEncrypt) ReceiveData(data []byte) error {
	// 与 client.go 相同的互斥约定：锁内只做状态与密钥变更，
	// RSA 运算、阻塞发送、下游分发放锁外（避免重入死锁）。
	m.mu.Lock()
	switch m.status {
	case ServerStatusWaitKey1:
		if len(data) != keySize {
			m.status = ServerStatusError
			m.mu.Unlock()
			return fmt.Errorf("ServerStatusWaitKey1 data len(%v) error", len(data))
		}
		m.TableDecrypt(data)
		m.Key1 = m.GetKeyByBytes(data, keySize)
		// 生成key2
		m.Key2 = m.GenKey()
		// 发送key2和公钥
		sendData := m.getKeyAndStrBytes(m.Key2, m.publicKeyBytes)
		m.Key1Encrypt(sendData)
		m.status = ServerStatusWaitCsNo
		m.mu.Unlock()
		return m.Pre().SendData(sendData)
	case ServerStatusWaitCsNo:
		m.mu.Unlock()
		_data, err := m.DecryptWithPrivateKey(data, m.privateKey)
		if err != nil {
			m.mu.Lock()
			m.status = ServerStatusError
			m.mu.Unlock()
			return err
		}
		if len(_data) != keySize {
			m.mu.Lock()
			m.status = ServerStatusError
			m.mu.Unlock()
			return fmt.Errorf("ServerStatusWaitCsNo data len(%v) error", len(_data))
		}
		m.mu.Lock()
		m.CsNo = m.GetKeyByBytes(_data, keySize)
		m.ScNo = m.GenKey()
		sendData := m.GetKeyBytes(m.ScNo, keySize)
		m.Key1Key2CsNoEncrypt(sendData)
		m.status = ServerStatusHandsFinish
		m.mu.Unlock()
		// 发送 ScNo 必须在 FireEvent(OnReady) 之前：对端要靠它完成握手
		if err := m.Pre().SendData(sendData); err != nil {
			return err
		}
		m.FireEvent(netMiddleware.MiddlewareEventOnReady)
		return nil
	case ServerStatusHandsFinish:
		m.mu.Unlock()
		m.GoNextCsNo()
		m.Key1Key2CsNoDecrypt(data)
		return m.Next().ReceiveData(data)
	default:
		status := m.status
		m.mu.Unlock()
		return fmt.Errorf("status %v error", status)
	}
}

func (m *ServerNetEncrypt) SendData(bytes []byte) error {
	m.mu.Lock()
	status := m.status
	m.mu.Unlock()
	if status != ServerStatusHandsFinish {
		return fmt.Errorf("status %v error", status)
	}
	m.sendMu.Lock()
	defer m.sendMu.Unlock()
	m.GoNextScNo()
	m.Key1Key2ScNoEncrypt(bytes)
	return m.Pre().SendData(bytes)
}
