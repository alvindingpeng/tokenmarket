package model

// WriteAnchor 是一张只有语义、没有业务数据的锚点表: 它唯一的作用是给
// "先读后写"的短事务当写锁把手(见 db.AcquireWriteLock)。
//
// 为什么需要一张专门的空表, 而不是随手拿 settings/users 之类的业务表打一下:
// 锁原语不该依赖某张业务表是否存在、是否被备份裁剪、是否被 ETL 清空 —— 业务表一旦
// 缺失, 事务会在第一句就因为 no such table 失败, 而失败点还在真正的业务逻辑之前。
// 锚点表由 AutoMigrate 无条件建出, 永远存在、永远为空。
//
// 表里不放任何行: 取锁用的是零命中的空更新(UPDATE ... WHERE 0), 不产生数据变更,
// 也不产生 WAL 页写入(实测 2000 次取锁 WAL 增长 0 字节)。
type WriteAnchor struct {
	ID uint `gorm:"primaryKey" json:"id"`
}

func (WriteAnchor) TableName() string { return "db_write_anchor" }
