package client

import (
	"errors"
	"sync"
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
	var wg sync.WaitGroup
	shakeHandsComplete := false
	wg.Add(1)
	c.lastMiddleware = netMiddlewareCommon.NewMiddlewareLast(func(data []byte) error {
		cb, msgID, msgData, err := c.codec.Decode(data)
		if err != nil {
			return err
		}
		return c.onMessage(cb, msgID, msgData)
	}, func() {
		shakeHandsComplete = true
		wg.Done()
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
	var err error
	c.connector.SetOnDisconnect(func() {
		firstMiddleware.FireEvent(netMiddleware.MiddlewareEventOnDisconnect)
		if c.onDisconnect != nil {
			c.onDisconnect()
		}
		if !shakeHandsComplete {
			err = errors.New("shake hands fail")
			wg.Done()
		}
	})
	c.connector.SetOnData(func(data []byte) error {
		return firstMiddleware.ReceiveData(data)
	})
	go func() {
		if err = c.connector.Connect(); err != nil {
			wg.Done()
		}
	}()
	wg.Wait()
	return err
}

func (c *netClient) Disconnect() error {
	return c.connector.Disconnect()
}

func (c *netClient) SendMessage(cb uint32, msgID uint32, data []byte) error {
	pkgData, err := c.codec.Encode(cb, msgID, data)
	if err != nil {
		return err
	}
	return c.lastMiddleware.SendData(pkgData)
}
