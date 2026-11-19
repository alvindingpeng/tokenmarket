package migrate

import (
	"fmt"

	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{Version: 40, Up: Migration040_ModelPriceCurrency})
}

// Migration040_ModelPriceCurrency 多币种供货价铺底(账面币种定为 USD)。
//
// fx_rates 由 model.FxRate 的 AutoMigration 建表, 这里只做两件 AutoMigration 做不了的事:
//
//  1. 存量 channel_models.price_currency 回填 "USD": 列是 AutoMigration 新建的, 存量行
//     全是空串。空串在读取侧已被 NormalizeCurrency 归一为 USD, 回填只是把默认值显式化,
//     让"这条价格是谁声明的币种"在库里一眼可读, 不依赖读取侧行为。
//  2. 幂等: UPDATE ... WHERE price_currency = ”, 第二次执行命中 0 行。
func Migration040_ModelPriceCurrency(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("channel_models") || !db.Migrator().HasColumn("channel_models", "price_currency") {
		return nil
	}
	result := db.Exec("UPDATE channel_models SET price_currency = 'USD' WHERE price_currency = ''")
	if result.Error != nil {
		return fmt.Errorf("backfill channel model price currency: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		log.Infof("migrate 40: backfilled price_currency=USD on %d channel models", result.RowsAffected)
	}
	return nil
}
