package client

import (
	"errors"
	"fmt"
	"sync/atomic"

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
	connector                netConnect.Connector
	codec                    netCodec.Codec
	onConnect                func()
	onReady                  func()
	onDisconnect             func()
	onMessage                func(cb uint32, t uint32, data []byte) error
	middlewareCreateFuncList []func() netMiddleware.Middleware
	lastMiddleware           netMiddleware.Middleware
}

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

func (c *netClient) Connect() error {
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
	return <-result
}

func (c *netClient) Disconnect() error {
	return c.connector.Disconnect()
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
