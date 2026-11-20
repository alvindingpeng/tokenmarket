package op

import (
	"path/filepath"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
)

// opTestDBSetup 统一的测试库生命周期: 先排空在途异步通知, 再换库, 收尾时先排空再关库。
//
// 为什么必须这样: internal/op 的所有测试共享包级全局 db。通知投递走后台 goroutine,
// 谁也不知道它跑完没有。若 setup 直接 InitDB, 上一用例的协程可能正把通知写进新库
// (断言被别人的数据污染), 或写向已关闭的旧库 (disk I/O error 1802/522);
// 收尾若不关库, 迟到的写还会让 t.TempDir 清理时报 "directory not empty"。
// 因此顺序固定为: drain -> InitDB ... cleanup: drain -> Close。
// cleanup 注册在 t.TempDir() 之后, 依赖 testing 的 LIFO 顺序, 保证关库先于删目录。
func opTestDBSetup(t *testing.T, dbFileName string) {
	t.Helper()
	NotificationDispatchDrain()
	AlertDispatchDrain()
	// 换库前必须清空进程级缓存: 它们活在进程里, 不属于任何一个库,
	// 换库后就是"旧库的数据"。不清的话 -shuffle=on 下用例顺序一变就互相污染。
	resetProcessCaches()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), dbFileName), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		// 先排空在途投递: 迟到的 goroutine 要么落进本库, 要么明确失败, 不带悬念跨用例。
		NotificationDispatchDrain()
		AlertDispatchDrain()
		resetProcessCaches()
		_ = db.Close()
	})
}
