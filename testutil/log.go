package testutil

import (
	"testing"

	zlog "ztunnel/engine/log"
)

// SilentLog 初始化一个静默（NONE 级别）的主日志到临时目录：
// 1) 避免业务代码在 log.Main() 为 nil 时 panic；
// 2) 消除测试输出噪音。
// 注意：logImp.Init 以相对路径拼接工作目录，因此这里用 t.Chdir 进入临时目录。
func SilentLog(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	l := zlog.NewLog()
	if err := l.Init("./log", "zt_"); err != nil {
		t.Fatalf("init silent log: %v", err)
	}
	l.SetLogLevel(zlog.NONE)
	zlog.SetMainLog(l)
	t.Cleanup(func() { _ = l.Uninit() })
}
