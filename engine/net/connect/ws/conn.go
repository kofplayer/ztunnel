package wsNetConnect

// import (
// 	"fmt"
// 	netConnect "ztunnel/engine/net/connect"

// 	"ztunnel/engine/queue"
// 	queueDef "ztunnel/engine/queue/def"

// 	// "github.com/gorilla/websocket"
// )

// func newConn(conn *websocket.Conn) *ConnWS {
// 	v := new(ConnWS)
// 	v.q = queue.NewQueue(32, false)
// 	v.conn = conn
// 	return v
// }

// type ConnWS struct {
// 	q                queueDef.Queue
// 	onDisconnectFunc netConnect.OnDisconnectFunc
// 	onDataFunc       netConnect.OnDataFunc
// 	conn             *websocket.Conn
// }

// func (this *ConnWS) RemoteAddr() string {
// 	if this.conn == nil || this.conn.RemoteAddr() == nil {
// 		return ""
// 	}
// 	return this.conn.RemoteAddr().String()
// }

// func (this *ConnWS) Disconnect() error {
// 	return this.q.Close()
// }

// func (this *ConnWS) SendData(data []byte) error {
// 	err := this.q.Enqueue(data)
// 	if err != nil {
// 		return err
// 	}
// 	return nil
// }

// func (this *ConnWS) SetOnDisconnect(onDisconnectFunc netConnect.OnDisconnectFunc) {
// 	this.onDisconnectFunc = onDisconnectFunc
// }

// func (this *ConnWS) SetOnData(onDataFunc netConnect.OnDataFunc) {
// 	this.onDataFunc = onDataFunc
// }

// func (this *ConnWS) receiverRun() {
// 	for {
// 		_, message, err := this.conn.ReadMessage()
// 		if err != nil {
// 			if !this.q.IsClose() {
// 				this.Disconnect()
// 				this.onDisconnectFunc()
// 			}
// 			return
// 		}
// 		fmt.Printf("net r !!!!!")
// 		err = this.onDataFunc(message)
// 		if err != nil {
// 			if !this.q.IsClose() {
// 				this.Disconnect()
// 				this.onDisconnectFunc()
// 			}
// 			return
// 		}
// 	}
// }

// func (this *ConnWS) senderRun() {
// 	for {
// 		data, ok := this.q.Dequeue()
// 		if !ok {
// 			this.conn.Close()
// 			return
// 		}
// 		err := this.conn.WriteMessage(websocket.BinaryMessage, data.([]byte))
// 		if err != nil {
// 			return
// 		}
// 	}
// }
