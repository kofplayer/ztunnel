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

// FireEvent 把事件交给链头派发（链头 NetMiddlewareFirst 负责重入排队）。
//
// 守卫的必要性（CRASH-04）：链头若不是 NetMiddlewareFirst——例如中间件被
// 单独使用、链未接完整、或 CreateMiddlewareFunc 工厂返回 nil 导致接线残缺——
// 那么 First() 会沿 pre 回溯不动、返回 m 自身，于是 `m.First().FireEvent(e)`
// 重新进入本方法，**无限自我递归 → stack overflow**。而 Go 的栈溢出属于
// fatal error，recover 拦不住，会直接杀死进程（比普通的 nil panic 更糟：
// 连"按连接关闭处理"的兜底语义都绕过了）。
// 链头是自己时就地派发 OnEvent，行为与"单节点链"一致。
func (m *MiddlewareBase) FireEvent(e MiddlewareEvent) {
	if f := m.First(); f != Middleware(m) {
		f.FireEvent(e)
		return
	}
	m.OnEvent(e)
}

func (m *MiddlewareBase) OnEvent(e MiddlewareEvent) {
	if m.next == nil {
		return
	}
	m.next.OnEvent(e)
}
