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

// CreateServerNetEncryptFunc 生成本服务端的 RSA 密钥对（所有连接共用一把，
// 代价是无前向保密：该私钥泄露或被破解后，全部历史抓包都可解密）。
//
// 修复 M-23：位数从 1024 提到 2048。1024 恰好踩在 Go 标准库
// rsa.EncryptOAEP 的最低强度下限上，官方一旦上调该下限，握手就会 100% 失败。
// 成本只是启动时一次密钥生成（约 50-200ms）。
func CreateServerNetEncryptFunc() netMiddleware.CreateMiddlewareFunc {
	privateKey, publicKeyBytes, err := genRSA(minRSABits)
	if err != nil {
		// 修复 L-4：原先 `return nil`，于是 nil 工厂会在建链时被调用
		// （`_m := f()` → nil 接口调用 / SetOnData 里对 nil 调 SetPre），
		// 崩溃点跑到某条连接的收发路径上。服务端拿不到自己的密钥就不该启动，
		// 在构造期立即失败。
		panic(fmt.Sprintf("type1NetEncrypt: cannot generate server RSA key: %v", err))
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
		key1, err := m.GetKeyByBytes(data, keySize)
		if err != nil {
			m.status = ServerStatusError
			m.mu.Unlock()
			return err
		}
		m.Key1 = key1
		key2, err := m.GenKey()
		if err != nil {
			m.status = ServerStatusError
			m.mu.Unlock()
			return err
		}
		m.Key2 = key2
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
		csNo, err := m.GetKeyByBytes(_data, keySize)
		if err != nil {
			m.mu.Lock()
			m.status = ServerStatusError
			m.mu.Unlock()
			return err
		}
		scNo, err := m.GenKey()
		if err != nil {
			m.mu.Lock()
			m.status = ServerStatusError
			m.mu.Unlock()
			return err
		}
		sendData := m.GetKeyBytes(scNo, keySize)

		m.mu.Lock()
		m.CsNo = csNo
		m.ScNo = scNo
		m.Key1Key2CsNoEncrypt(sendData)
		m.mu.Unlock()

		// 修复 PROTO-01：握手最后一帧必须在**已占住 sendMu** 的情况下再翻到
		// HandsFinish 并发送。
		//
		// 原先的顺序是"置 status=HandsFinish → Unlock → 不带 sendMu 地发送"。
		// 在置位与发送之间，任何 goroutine 的 SendMessage 都能通过 SendData 的
		// status 检查、抢先入队一帧**应用数据**（SendData 是无界队列 Enqueue）。
		// 客户端此时仍停在 WaitScNo 且不校验长度，会把那帧的前 8 字节当成 ScNo
		// 接受 → 双方序号永久错开。
		//
		// 持 sendMu 后顺序被锁死：并发的 SendData 要么还没看到 HandsFinish 而被拒，
		// 要么看到了（即发生在我们翻转之后）然后阻塞在 sendMu 上，直到 ScNo 已入队。
		//
		// 锁序约定：**sendMu → mu**。切勿持 mu 再去拿 sendMu（SendData 是
		// 先释放 mu 再拿 sendMu，不构成嵌套），否则会与这里形成 AB-BA 死锁。
		m.sendMu.Lock()
		m.mu.Lock()
		m.status = ServerStatusHandsFinish
		m.mu.Unlock()
		// 发送 ScNo 必须在 FireEvent(OnReady) 之前：对端要靠它完成握手
		err = m.Pre().SendData(sendData)
		m.sendMu.Unlock()
		if err != nil {
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
