package netConnect

type OnAcceptFunc func(Conn)

type Acceptor interface {
	// Listen 预绑定监听端口但不进入 Accept 循环，
	// 供调用方在 Start 前确认端口可用（如建隧道时同步应答成功/失败）。
	Listen() error
	Start() error
	Stop() error
	SetOnAccept(OnAcceptFunc)
}
