package migrate

import (
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{Version: 39, Up: Migration039_Reserved})
}

// Migration039_Reserved 是**空占位**, 只占住版本号 39, 不改任何 schema。
//
// 为什么预留: 版本 39 已在一次生产部署中被记成 success —— 那次部署跑的是
// "删掉 billing_settlement_tasks 上遗留单列 status 索引"的迁移。索引删除本身后来**撤回了**
// (理由见 internal/model/billing_recovery.go 上 Status 字段的注释: 复合索引 (status, updated_at)
// 已让恢复列表三个分桶全部走纯索引扫描, 而单列索引只剩 168 KB、能力被复合索引覆盖,
// 删它量不到收益, 却要让每次部署多跑一条 DROP INDEX), 但 migration_records 里那行
// (39, 1) 已经落地, 生产库上收不回来。
//
// 若日后有人补一个**真正**的 39, 记录表会判定"已成功"而静默跳过它 —— 那是最难查的一类
// 迁移事故: 代码里写了, 测试也过, 线上就是不生效。留着这个空迁移把号占住, 后来者从 40 起编号。
//
// 幂等: 不改 schema, 重复执行与首次执行完全一致。
func Migration039_Reserved(_ *gorm.DB) error {
	log.Debugf("migrate 39: no-op placeholder (version number reserved)")
	return nil
}
