package type1NetEncrypt

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	netMiddleware "ztunnel/engine/net/middleware"
	len4Data "ztunnel/engine/net/middleware/package/len4Data"
	"ztunnel/testutil"
)

// ---- CRYPT-01：掩码必须用到密钥的全部 64 bit ----

func maskOf(m *BaseNetEncrypt, k Key) [keySize]byte {
	data := make([]byte, keySize) // 与全零 XOR 即得掩码本身
	m.KeyMaskData(data, k)
	var out [keySize]byte
	copy(out[:], data)
	return out
}

// 回归 CRYPT-01：掩码字节此前取 `(key >> i) & 0xFF`，i 是 0..7 的下标——
// 这是**按位**位移而非按字节位移，于是 8 个掩码字节只覆盖 key 的 bit0..bit14，
// 高 49 bit 被完全丢弃。实测（reports/evidence/CRYPT-01_mask_entropy.go）：
// 64 位密钥空间只产生 32768 种掩码，且低 15 bit 为 0 的 key 产生**全零掩码**，
// 该连接数据原样明文上网（概率 1/32768）。
func TestType1_KeyMaskData_UsesAll64Bits(t *testing.T) {
	m := &BaseNetEncrypt{}

	// 只有高 49 bit 不同的两个 key，必须产生不同掩码
	a := maskOf(m, Key(0x0001000000000000))
	b := maskOf(m, Key(0x0002FFFFFFFFFFFF))
	testutil.True(t, a != b,
		fmt.Sprintf("CRYPT-01 未修复：仅高 49 bit 不同的两个 key 产生了相同掩码 %v / %v", a, b))

	// 低 15 bit 为 0、高位非 0 的 key 绝不能退化为全零掩码
	zeroLow := maskOf(m, Key(0xDEADBEEF00000000))
	testutil.True(t, zeroLow != [keySize]byte{},
		"CRYPT-01 未修复：低 15 bit 为 0 的 key 产生全零掩码，数据明文直通")

	// 非零 key 一律不得产生全零掩码
	for _, k := range []Key{1, 0x8000, 0xFFFF, 0x10000, Key(1) << 63} {
		testutil.True(t, maskOf(m, k) != [keySize]byte{},
			fmt.Sprintf("CRYPT-01 未修复：非零 key %x 产生了全零掩码", uint64(k)))
	}
}

// 回归 CRYPT-01 的熵度量：不同 key 应铺满 64 位空间，而不是恒为 2^15 种掩码。
func TestType1_MaskEntropy(t *testing.T) {
	m := &BaseNetEncrypt{}
	seen := map[[keySize]byte]bool{}
	for i := 0; i < 100000; i++ {
		seen[maskOf(m, Key(uint64(i)*0x9E3779B97F4A7C15+1))] = true
	}
	// 修复前这里恒为 32768
	testutil.True(t, len(seen) > 90000,
		fmt.Sprintf("CRYPT-01 未修复：10 万个不同 key 只产生了 %d 种掩码（期望接近 1:1）", len(seen)))
}

// ---- CRYPT-02：密钥材料必须来自 CSPRNG ----

// 回归 CRYPT-02：本包的会话密钥此前由 math/rand 生成。
// 这是一条**源码级**守卫——math/rand 与 crypto/rand 的输出无法廉价地统计区分，
// 但"密钥材料不得来自 math/rand"是绝对要求，用导入检查直接钉住最可靠。
func TestType1_KeysMustNotComeFromMathRand(t *testing.T) {
	src, err := os.ReadFile("base.go")
	testutil.NoError(t, err)
	testutil.True(t, !strContains(string(src), `"math/rand"`),
		"CRYPT-02 未修复：base.go 仍在导入 math/rand 用于生成会话密钥")
	testutil.True(t, strContains(string(src), "cryptoRand.Reader"),
		"GenKey 必须从 crypto/rand 取随机数")
}

func strContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// GenKey 必须互不相同；失败只能以 error 表达，**不得**降级返回 0
// （Key=0 → 掩码全零 → 明文）。
func TestType1_GenKey_Unique(t *testing.T) {
	m := &BaseNetEncrypt{}
	seen := map[Key]bool{}
	for i := 0; i < 1000; i++ {
		k, err := m.GenKey()
		testutil.NoError(t, err)
		testutil.True(t, k != 0, "GenKey 失败时不得返回 0（Key=0 → 掩码全零 → 明文）")
		testutil.True(t, !seen[k], "GenKey 产生重复密钥")
		seen[k] = true
	}
}

// ---- M-06：握手帧长度必须被校验 ----

// 回归 M-06：client 的 WaitScNo 分支此前**完全不校验长度**（server 侧同类分支
// 都有检查），短帧会被静默补零成"高位全零的 ScNo"并直接置 HandsFinish
// → 双方序号永久错开，且攻击者可控字节进入掩码。
//
// 本文件在 type1NetEncrypt 包内，可直接把状态机构造成 WaitScNo 来精准命中该分支
// （Key1 由 client 在 OnEvent 内部生成，测试无法预知，故不走完整握手）。
func TestType1_ClientRejectsShortScNoFrame(t *testing.T) {
	newWaitScNo := func() *ClientNetEncrypt {
		c := &ClientNetEncrypt{status: ClientStatusWaitScNo}
		c.Key1, c.Key2, c.CsNo = Key(0x1111222233334444), Key(0x5555666677778888), Key(0x9999AAAABBBBCCCC)
		return c
	}

	c := newWaitScNo()
	err := c.ReceiveData([]byte{1, 2, 3})
	testutil.Error(t, err, "M-06 未修复：client 接受了长度不足的 ScNo 帧")
	testutil.Equal(t, int(ClientStatusError), int(c.status),
		"拒绝短帧后状态机必须失效，不得滞留在 WaitScNo")

	// 状态失效后不得再被当作"握手完成"继续
	testutil.Error(t, c.ReceiveData([]byte{1, 2, 3, 4, 5, 6, 7, 8}),
		"M-06 未修复：拒绝短帧后状态机仍可继续")

	// 对照：恰好 8 字节的合法长度必须被接受并进入 HandsFinish
	ok := newWaitScNo()
	testutil.NoError(t, ok.ReceiveData([]byte{1, 2, 3, 4, 5, 6, 7, 8}),
		"长度正确的 ScNo 帧应被接受")
	testutil.Equal(t, int(ClientStatusHandsFinish), int(ok.status))

	// 超过 8 字节同样必须拒绝（此前会被静默截取前 8 字节）
	long := newWaitScNo()
	testutil.Error(t, long.ReceiveData(make([]byte, 9)), "M-06 未修复：超长 ScNo 帧被静默接受")
}

// GetKeyByBytes / getKeyAndStrByBytes 不得对畸形输入静默补零。
func TestType1_GetKeyByBytes_RejectsShort(t *testing.T) {
	m := &BaseNetEncrypt{}
	_, err := m.GetKeyByBytes([]byte{1, 2, 3}, keySize)
	testutil.Error(t, err, "M-06 未修复：GetKeyByBytes 对短数据静默补零")
	_, _, err = m.getKeyAndStrByBytes([]byte{1, 2, 3})
	testutil.Error(t, err, "M-06 未修复：getKeyAndStrByBytes 对畸形帧返回 0 密钥而不报错")
}

// ---- M-23：拒绝弱对端公钥 ----

// x509.ParsePKIXPublicKey 本身**接受**弱模数，此前唯一的防线是
// rsa.EncryptOAEP 的隐式下限（Go 一旦上调就会全线握手失败）。
func TestType1_RejectsWeakPeerPublicKey(t *testing.T) {
	m := &BaseNetEncrypt{}

	for _, bits := range []int{512, 1024} {
		priv, err := rsa.GenerateKey(rand.Reader, bits)
		if err != nil {
			t.Skipf("本机无法生成 %d 位密钥: %v", bits, err)
		}
		b, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
		testutil.NoError(t, err)
		_, err = m.GetPublicKeyByBytes(b)
		testutil.Error(t, err,
			fmt.Sprintf("M-23 未修复：%d 位对端公钥被接受（策略要求 >= %d 位）", bits, minRSABits))
	}

	priv, err := rsa.GenerateKey(rand.Reader, minRSABits)
	testutil.NoError(t, err)
	b, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	testutil.NoError(t, err)
	pub, err := m.GetPublicKeyByBytes(b)
	testutil.NoError(t, err, "2048 位公钥应被接受")
	testutil.Equal(t, minRSABits, pub.N.BitLen())
}

// ---- PROTO-01：服务端发送与握手末帧的交付顺序 ----

// 回归 PROTO-01：server 的握手最后一帧（ScNo）此前在**已置 status=HandsFinish
// 之后、且未持 sendMu** 的情况下发送。窗口内任何 goroutine 的 SendMessage
// 都能通过 status 检查并抢先交付一帧应用数据（SendData 只是无界队列 Enqueue），
// 而客户端仍停在 WaitScNo 且不校验长度 → 会把那帧前 8 字节当成 ScNo 接受
// → 双方序号永久错开、后续全部消息损坏。
//
// 做法：在握手**开始之前**就用多个 goroutine 持续尝试服务端发送（未完成时
// SendData 返回 error，忽略即可），一旦状态翻到 HandsFinish 就会命中那个窗口；
// 握手完成后校验客户端收到的条数与"被接受"的条数一致且每条载荷完好。
//
// ⚠️ 与仓库里 #6 用例同性质：**概率性**检出。窗口很窄，单轮不一定命中，故跑多轮；
// 配 -race 时调度扰动更大。它能守住"发送被移出 sendMu"这类回归，但不能证明无缺陷。
func TestType1_ServerSendDuringHandshake_NoDesync(t *testing.T) {
	// RSA 密钥对只生成一次并跨轮复用：genRSA(2048) 每轮都要上百毫秒。
	srvFactory := CreateServerNetEncryptFunc()
	for round := 0; round < 12; round++ {
		if msg := oneServerSendRound(t, srvFactory); msg != "" {
			t.Fatalf("PROTO-01 未修复（第 %d 轮）：%s", round, msg)
		}
	}
}

func oneServerSendRound(t *testing.T, srvFactory netMiddleware.CreateMiddlewareFunc) string {
	t.Helper()

	const goroutines = 8

	c1, s1 := net.Pipe()
	defer func() {
		_ = c1.Close()
		_ = s1.Close()
	}()

	cRec := &testutil.Recorder{}
	cReady := make(chan struct{})
	sReady := make(chan struct{})

	cFirst, _ := testutil.BuildChain(func(b []byte) error { _, err := c1.Write(b); return err },
		[]netMiddleware.CreateMiddlewareFunc{len4Data.NewMiddleware, NewClientNetEncrypt},
		cRec.Receive, func() { close(cReady) })
	sFirst, sLast := testutil.BuildChain(func(b []byte) error { _, err := s1.Write(b); return err },
		[]netMiddleware.CreateMiddlewareFunc{len4Data.NewMiddleware, srvFactory},
		func([]byte) error { return nil }, func() { close(sReady) })

	pumpErrC := testutil.Pump(c1, cFirst)
	pumpErrS := testutil.Pump(s1, sFirst)

	var accepted atomic.Uint32
	var seq atomic.Uint32
	stop := make(chan struct{})
	var wg sync.WaitGroup
	stopped := false
	halt := func() {
		if !stopped {
			stopped = true
			close(stop)
		}
	}

	// 握手尚未发生，就开始轰炸服务端发送侧
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = seq.Add(1)
				if err := sLast.SendData(testutil.SeqPayload(uint32(seq.Load()), 32)); err == nil {
					accepted.Add(1)
				}
			}
		}()
	}

	cFirst.FireEvent(netMiddleware.MiddlewareEventOnConnect)

	failed := ""
	for _, ch := range []chan struct{}{sReady, cReady} {
		select {
		case <-ch:
		case err := <-pumpErrC:
			failed = fmt.Sprintf("握手期间 client 链错误: %v", err)
		case err := <-pumpErrS:
			failed = fmt.Sprintf("握手期间 server 链错误: %v", err)
		case <-time.After(10 * time.Second):
			failed = "握手 10s 未完成"
		}
		if failed != "" {
			break
		}
	}

	time.Sleep(20 * time.Millisecond)
	halt()
	wg.Wait()
	if failed != "" {
		return failed
	}

	want := accepted.Load()
	if !testutil.Eventually(t, 5*time.Second, func() bool {
		return uint32(cRec.Count()) == want
	}) {
		return fmt.Sprintf("收到 %d 条 / 发送侧已接受 %d 条（失步或消息丢失）", cRec.Count(), want)
	}
	for i := 0; i < cRec.Count(); i++ {
		if _, err := testutil.VerifySeqPayload(cRec.Get(i)); err != nil {
			return fmt.Sprintf("第 %d 条消息损坏: %v（握手末帧被应用数据插队的典型症状）", i, err)
		}
	}
	return ""
}

func encodeLen4(body []byte) []byte {
	n := len(body)
	out := make([]byte, 4, 4+n)
	out[0] = byte(n >> 24)
	out[1] = byte(n >> 16)
	out[2] = byte(n >> 8)
	out[3] = byte(n)
	return append(out, body...)
}
