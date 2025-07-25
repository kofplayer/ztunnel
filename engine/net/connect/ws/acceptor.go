package wsNetConnect

// import (
// 	"fmt"
// 	"net/http"
// 	netConnect "ztunnel/engine/net/connect"

// 	"github.com/gorilla/websocket"
// )

// func NewAcceptor() *AcceptorWS {
// 	v := new(AcceptorWS)
// 	return v
// }

// type AcceptorWS struct {
// 	onAcceptFunc netConnect.OnAcceptFunc
// 	host         string
// 	port         uint16
// 	path         string
// }

// func (this *AcceptorWS) Start() error {

// 	var upgrader = websocket.Upgrader{
// 		ReadBufferSize:  1024,
// 		WriteBufferSize: 1024,
// 		CheckOrigin:     func(r *http.Request) bool { return true },
// 	}

// 	http.HandleFunc(this.path, func(w http.ResponseWriter, r *http.Request) {
// 		conn, err := upgrader.Upgrade(w, r, nil)
// 		if err != nil {
// 			return
// 		}
// 		c := newConn(conn)
// 		this.onAcceptFunc(c)
// 		go c.receiverRun()
// 		go c.senderRun()
// 	})
// 	return http.ListenAndServe(fmt.Sprintf("%v:%v", this.host, this.port), nil)
// }

// func (this *AcceptorWS) Stop() error {
// 	return nil
// }

// func (this *AcceptorWS) SetOnAccept(onAcceptFunc netConnect.OnAcceptFunc) {
// 	this.onAcceptFunc = onAcceptFunc
// }

// func (this *AcceptorWS) SetAddress(host string, port uint16, path string) {
// 	this.host = host
// 	this.port = port
// 	this.path = path
// }
