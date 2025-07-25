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
		fmt.Println("生成私钥失败:", err)
		return nil, nil, err
	}

	// 将公钥序列化为PEM格式
	publicKeyBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		fmt.Println("导出公钥失败:", err)
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
	switch m.status {
	case ServerStatusWaitKey1:
		if len(data) != keySize {
			m.status = ServerStatusError
			return fmt.Errorf("ServerStatusWaitKey1 data len(%v) error", len(data))
		}
		m.TableDecrypt(data)
		m.Key1 = m.GetKeyByBytes(data, keySize)
		// 生成key2
		m.Key2 = m.GenKey()
		// 发送key2和公钥
		sendData := m.getKeyAndStrBytes(m.Key2, m.publicKeyBytes)
		m.Key1Encrypt(sendData)
		m.Pre().SendData(sendData)
		m.status = ServerStatusWaitCsNo
		return nil
	case ServerStatusWaitCsNo:
		_data, err := m.DecryptWithPrivateKey(data, m.privateKey)
		if err != nil {
			m.status = ServerStatusError
			return err
		}
		if len(_data) != keySize {
			m.status = ServerStatusError
			return fmt.Errorf("ServerStatusWaitCsNo data len(%v) error", len(_data))
		}
		m.CsNo = m.GetKeyByBytes(_data, keySize)
		m.ScNo = m.GenKey()
		sendData := m.GetKeyBytes(m.ScNo, keySize)
		m.Key1Key2CsNoEncrypt(sendData)
		m.Pre().SendData(sendData)
		m.status = ServerStatusHandsFinish
		m.FireEvent(netMiddleware.MiddlewareEventOnReady)
		return nil
	case ServerStatusHandsFinish:
		m.GoNextCsNo()
		m.Key1Key2CsNoDecrypt(data)
		return m.Next().ReceiveData(data)
	default:
		return fmt.Errorf("status %v error", m.status)
	}
}

func (m *ServerNetEncrypt) SendData(bytes []byte) error {
	if m.status != ServerStatusHandsFinish {
		return fmt.Errorf("status %v error", m.status)
	}
	m.GoNextScNo()
	m.Key1Key2ScNoEncrypt(bytes)
	return m.Pre().SendData(bytes)
}
