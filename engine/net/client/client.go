package client

import (
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	netCodec "ztunnel/engine/net/codec"
	netConnect "ztunnel/engine/net/connect"
	netMiddleware "ztunnel/engine/net/middleware"
	netMiddlewareCommon "ztunnel/engine/net/middleware/common"
)

func NewNetClient() NetClient {
	v := new(netClient)
	return v
}

type NetClient interface {
	SetConnector(connector netConnect.Connector)
	SetCodec(codec netCodec.Codec)
	SetOnConnect(func())
	SetOnReady(func())
	SetOnDisconnect(func())
	SetOnMessage(func(cb uint32, msgID uint32, data []byte) error)
	Connect() error
	Disconnect() error
	SendMessage(cb uint32, msgID uint32, data []byte) error
	AddMiddleware(f func() netMiddleware.Middleware)
}

type netClient struct {
	// state 守卫 Connect/Disconnect 的调用时机（报告 M-12）。
	//
	// 重复 Connect() 的后果曾被"调用方每轮新建实例"侥幸掩盖：它会重建一条
	// 中间件链并覆盖 lastMiddleware（主 goroutine 写、**旧**连接的 receiver
	// goroutine 并发读），再对同一个 ConnectorSocket 拨号覆盖底层 conn，于是
	// 旧 fd 与两个 goroutine 永久残留；而 SetOnDisconnect 被改指向新链，旧连接
	// 断开时的清理会打到新链上。靠自律不是靠类型。
	state atomic.Int32

	connector                netConnect.Connector
	codec                    netCodec.Codec
	onConnect                func()
	onReady                  func()
	onDisconnect             func()
	onMessage                func(cb uint32, t uint32, data []byte) error
	middlewareCreateFuncList []func() netMiddleware.Middleware
	lastMiddleware           netMiddleware.Middleware
}

const (
	stateIdle       int32 = 0
	stateConnecting int32 = 1
	stateReady      int32 = 2
	// stateDone 是终态：这个实例（连同它的 connector）已经用过了。
	//
	// 为什么不允许"断开后再 Connect"：那会对**同一个** ConnectorSocket 二次
	// 拨号并覆盖底层 conn，旧 fd 与两个 goroutine 永久残留——正是 M-12 要防的
	// 行为。守卫若留了这个后门就形同虚设。重连必须构造新实例
	// （cmd/client 的重连循环本来就是这么写的）。
	stateDone int32 = 3
)

func (c *netClient) SetConnector(connector netConnect.Connector) {
	c.connector = connector
}

func (c *netClient) SetCodec(codec netCodec.Codec) {
	c.codec = codec
}

func (this *netClient) AddMiddleware(f func() netMiddleware.Middleware) {
	this.middlewareCreateFuncList = append(this.middlewareCreateFuncList, f)
}

func (c *netClient) SetOnConnect(f func()) {
	c.onConnect = f
}

func (c *netClient) SetOnReady(f func()) {
	c.onReady = f
}

func (c *netClient) SetOnDisconnect(f func()) {
	c.onDisconnect = f
}

func (c *netClient) SetOnMessage(f func(cb uint32, msgID uint32, data []byte) error) {
	c.onMessage = f
}

// HandshakeTimeout 是等待握手完成的最长时间。
//
// 修复 DOS-01（客户端侧）：原先 `return <-result` 没有任何超时。对端接受 TCP
// 连接后不回任何数据（或中间设备把后续包吞掉）时，result 永远无人写入，
// Connect() 就**永久阻塞**：调用方 outClient.Start() 挂死，dial/receiver/sender
// 三个 goroutine 与整条中间件链全部泄漏，而进程看起来还活着。
// 设为包级变量是为了让入口（-handshake_timeout）在启动时一次性覆盖；运行期不要改它。
var HandshakeTimeout = 30 * time.Second

// Connect 组装中间件链、拨号并等待握手完成。
//
// 只能从 Idle 状态调用一次：Connecting/Ready/Done 时调用会直接报错，而不是悄悄
// 造出两条链、漏掉旧连接的 fd 与 goroutine（报告 M-12）。
func (c *netClient) Connect() error {
	if !c.state.CompareAndSwap(stateIdle, stateConnecting) {
		return fmt.Errorf("client: Connect allowed once per instance, not idle (state=%d); "+
			"construct a new client to reconnect", c.state.Load())
	}
	err := c.connect()
	if err != nil {
		// 落终态而不是回 Idle：拨号可能已经建立了半开连接，再 Connect 一次
		// 就是对同一个 ConnectorSocket 二次拨号。
		c.state.Store(stateDone)
		return err
	}
	c.state.Store(stateReady)
	return nil
}

func (c *netClient) connect() error {
	firstMiddleware := netMiddlewareCommon.NewMiddlewareFirst(func(data []byte) error {
		return c.connector.SendData(data)
	})
	currentMiddleware := firstMiddleware
	for _, f := range c.middlewareCreateFuncList {
		_m := f()
		_m.SetPre(currentMiddleware)
		currentMiddleware.SetNext(_m)
		currentMiddleware = _m
	}
	// 连接结果：拨号失败 / 握手前断开 / 握手完成，三者先到先得。
	// 用 cap 1 channel + 非阻塞发送取代原先跨 goroutine 写的共享 err 变量
	// （-race 实测 err 存在数据竞争，报告 #10）。
	result := make(chan error, 1)
	var handshakeComplete int32
	c.lastMiddleware = netMiddlewareCommon.NewMiddlewareLast(func(data []byte) error {
		cb, msgID, msgData, err := c.codec.Decode(data)
		if err != nil {
			return err
		}
		if c.onMessage == nil {
			return nil
		}
		return c.onMessage(cb, msgID, msgData)
	}, func() {
		atomic.StoreInt32(&handshakeComplete, 1)
		select {
		case result <- nil:
		default:
		}
		if c.onReady != nil {
			c.onReady()
		}
	})
	c.lastMiddleware.SetPre(currentMiddleware)
	currentMiddleware.SetNext(c.lastMiddleware)

	c.connector.SetOnConnect(func() {
		firstMiddleware.FireEvent(netMiddleware.MiddlewareEventOnConnect)
		if c.onConnect != nil {
			c.onConnect()
		}
	})
	c.connector.SetOnDisconnect(func() {
		firstMiddleware.FireEvent(netMiddleware.MiddlewareEventOnDisconnect)
		// 断开即终态：落到 Done，防止有人拿同一个实例重连（报告 M-12）。
		c.state.CompareAndSwap(stateReady, stateDone)
		c.state.CompareAndSwap(stateConnecting, stateDone)
		if c.onDisconnect != nil {
			c.onDisconnect()
		}
		if atomic.LoadInt32(&handshakeComplete) == 0 {
			select {
			case result <- errors.New("shake hands fail"):
			default:
			}
		}
	})
	c.connector.SetOnData(func(data []byte) error {
		return firstMiddleware.ReceiveData(data)
	})
	go func() {
		// 必须 recover：Connect() 会在本 goroutine 内同步执行 onConnectFunc
		// （= FireEvent(OnConnect) + 业务 onConnect），type1 的状态守卫会 panic。
		// server 侧有 safeAccept 兜底，client 侧此前完全没有 → 单连接异常杀死
		// 整个进程（报告 ROBUST-01）。
		// ⚠️ 恢复后**必须**向 result 投递：漏投递 = Connect() 永久阻塞，
		// 比 panic 更难查（连接既不成功也不报错，只是挂着）。
		defer func() {
			if r := recover(); r != nil {
				select {
				case result <- fmt.Errorf("connect panic: %v", r):
				default:
				}
			}
		}()
		if err := c.connector.Connect(); err != nil {
			select {
			case result <- err:
			default:
			}
		}
	}()
	// 三者先到先得：拨号失败 / 握手完成 / 握手前断开。
	// 加超时是因为对端可能接受 TCP 后一言不发（见 HandshakeTimeout 的说明）——
	// 没有它，这条 goroutine 会永久挂在 <-result 上。
	timer := time.NewTimer(HandshakeTimeout)
	defer timer.Stop()

	select {
	case err := <-result:
		// 这里**不**主动 Disconnect：能走到这条分支只有两种情况——
		// 拨号失败（根本没有连接需要回收），或断开流程已经在跑了
		// （对端关闭触发 onDisconnectFunc 后才把错误塞进 result）。
		// 多调一次 Disconnect 会派发 OnDisconnect，于是 inclient 会为一个
		// **从未建立过**的转发补发 ConnectDelete，服务端凭空多收一帧删除消息。
		return err
	case <-timer.C:
		// 只有这条路径确实持有一个"已建立但永远不会完成握手"的连接，必须回收
		_ = c.connector.Disconnect()
		return fmt.Errorf("handshake timeout after %v (peer accepted the connection "+
			"but never completed the handshake)", HandshakeTimeout)
	}
}

func (c *netClient) Disconnect() error {
	err := c.connector.Disconnect()
	// 主动断开同样是终态：一个实例只负责一次连接（报告 M-12）
	c.state.CompareAndSwap(stateReady, stateDone)
	c.state.CompareAndSwap(stateConnecting, stateDone)
	return err
}

func (c *netClient) SendMessage(cb uint32, msgID uint32, data []byte) error {
	// lastMiddleware 只在 Connect() 内赋值：未连接（或 Connect 已失败）就
	// SendMessage 会对 nil 接口调用 → panic。与 netSession.SendMessage 的
	// 既有处理保持一致，返回 error（报告 L-12）。
	if c.lastMiddleware == nil {
		return errors.New("client not connected")
	}
	pkgData, err := c.codec.Encode(cb, msgID, data)
	if err != nil {
		return err
	}
	return c.lastMiddleware.SendData(pkgData)
}
