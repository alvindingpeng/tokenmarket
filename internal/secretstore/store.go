package secretstore

import (
	"fmt"
	"sync"
)

// MasterKey 是本进程使用的信封加密主密钥: KEK 材料 + 信封里的密钥 ID。
type MasterKey struct {
	kek   []byte
	keyID string
}

// KeyID 返回信封中标记的密钥 ID。
func (k *MasterKey) KeyID() string { return k.keyID }

// Wipe 抹掉进程内保留的密钥材料。仅用于测试与显式关闭; 进程退出由操作系统回收。
func (k *MasterKey) Wipe() {
	for i := range k.kek {
		k.kek[i] = 0
	}
}

var (
	activeMu sync.RWMutex
	active   *MasterKey
)

// Init 从外部来源加载主密钥并设为进程全局。失败时全局保持未初始化状态:
// 宁可让后续写入/迁移全部拒绝, 也不退回明文。
func Init(cfg KEKConfig) error {
	key, err := LoadKEK(cfg)
	if err != nil {
		return err
	}
	activeMu.Lock()
	active = key
	activeMu.Unlock()
	return nil
}

// Active 返回进程全局主密钥; 未初始化时返回 nil。
// GORM 集成层在 nil 时保持透传(单测等无钥场景), 生产入口(cmd start / secret 命令)必须先 Init 并在失败时退出。
func Active() *MasterKey {
	activeMu.RLock()
	defer activeMu.RUnlock()
	return active
}

// Reset 清空进程全局主密钥, 仅供测试隔离使用。
func Reset() {
	activeMu.Lock()
	if active != nil {
		active.Wipe()
		active = nil
	}
	activeMu.Unlock()
}

// Require 返回进程全局主密钥, 未初始化时报错。迁移等必须在加密态下执行的流程用它。
func Require() (*MasterKey, error) {
	key := Active()
	if key == nil {
		return nil, fmt.Errorf("secret master key is not initialized")
	}
	return key, nil
}
