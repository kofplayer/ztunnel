package netMiddleware

type MiddlewareEvent int32

const (
	MiddlewareEventOnConnect    MiddlewareEvent = 1
	MiddlewareEventOnDisconnect MiddlewareEvent = 2
	MiddlewareEventOnReady      MiddlewareEvent = 3
)

type Middleware interface {
	SetPre(Middleware)
	SetNext(Middleware)
	Pre() Middleware
	Next() Middleware
	FireEvent(MiddlewareEvent)
	OnEvent(MiddlewareEvent)
	ReceiveData([]byte) error
	SendData([]byte) error
}

type CreateMiddlewareFunc func() Middleware

type MiddlewareBase struct {
	pre  Middleware
	next Middleware
}

func (m *MiddlewareBase) SetPre(pre Middleware) {
	m.pre = pre
}

func (m *MiddlewareBase) SetNext(next Middleware) {
	m.next = next
}

func (m *MiddlewareBase) Pre() Middleware {
	return m.pre
}

func (m *MiddlewareBase) Next() Middleware {
	return m.next
}

func (m *MiddlewareBase) ReceiveData(bytes []byte) error {
	return m.next.ReceiveData(bytes)
}

func (m *MiddlewareBase) SendData(bytes []byte) error {
	return m.pre.SendData(bytes)
}

func (m *MiddlewareBase) First() Middleware {
	var v Middleware = m
	for v.Pre() != nil {
		v = v.Pre()
	}
	return v
}

func (m *MiddlewareBase) FireEvent(e MiddlewareEvent) {
	m.First().FireEvent(e)
}

func (m *MiddlewareBase) OnEvent(e MiddlewareEvent) {
	if m.next == nil {
		return
	}
	m.next.OnEvent(e)
}
