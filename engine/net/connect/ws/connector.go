package wsNetConnect

// import (
// 	"fmt"
// 	"net/url"
// 	netConnect "ztunnel/engine/net/connect"

// 	"github.com/gorilla/websocket"
// )

// func NewConnector() *ConnectorWS {
// 	v := new(ConnectorWS)
// 	return v
// }

// type ConnectorWS struct {
// 	onConnectFunc netConnect.OnConnectFunc
// 	*ConnWS
// 	host string
// 	port uint16
// 	path string
// }

// func (this *ConnectorWS) Connect() error {
// 	u := url.URL{Scheme: "ws", Host: fmt.Sprintf("%v:%v", this.host, this.port), Path: this.path}
// 	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
// 	if err != nil {
// 		return err
// 	}
// 	this.ConnWS = newConn(conn)
// 	go this.receiverRun()
// 	go this.senderRun()
// 	this.onConnectFunc()
// 	return nil
// }

// func (this *ConnectorWS) SetOnConnect(onConnectFunc netConnect.OnConnectFunc) {
// 	this.onConnectFunc = onConnectFunc
// }

// func (this *ConnectorWS) SetAddress(host string, port uint16, path string) {
// 	this.host = host
// 	this.port = port
// 	this.path = path
// }
