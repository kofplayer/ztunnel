package netCodec

// Codec 决定消息头在字节流里的格式。
//
// 所有权契约（报告 M-16，改动任何编解码器或中间件前必读）：
//
//   - Encode 的返回值会被沿着中间件链一路传到 **某条连接的异步发送队列**
//     （`ConnSocket.SendData` 只入队、不拷贝，`senderRun` 稍后在另一条 goroutine
//     上写出）。因此 Encode 要么**新分配**返回缓冲，要么保证其底层数组在整帧
//     被写出之前不会被别人改写。绝不能就地修改入参 data：入参可能正被另一条
//     连接的接收缓冲持有。
//   - Decode 返回的 data 是入参 pkg 的**视图**，只在 OnMessage 回调期间有效；
//     需要跨回调周期保留就必须自行拷贝。
//
// codec_data 是零拷贝透传，本身不满足"新分配"这一条；它之所以安全，是因为
// 使用它的两条链路（控制通道经 len4Data、数据通道经 fullData）里，长度前缀
// 中间件在接收侧已经把数据拷进自有缓冲，且只向后追加、单调前移 head，从不改写
// 已交付的区间。这个前提没有类型系统兜住，只由 len4Data 的拷贝与别名回归用例
// 兜住——任何"给分包层加环形复用""给 codec 换 slices.Grow""换缓冲池"的优化都会
// 立刻把它变成静默数据损坏（正是报告 #5 那一类）。
type Codec interface {
	Encode(cb uint32, t uint32, data []byte) ([]byte, error)
	Decode(pkg []byte) (uint32, uint32, []byte, error)
}
