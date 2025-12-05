package db

import (
	"strings"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"gorm.io/gorm/logger"
)

var db *gorm.DB

func InitDB(path string, debug bool) error {
	var err error
	params := []string{
		"_journal_mode=WAL",
		"_synchronous=NORMAL",
		"_cache_size=10000",
		"_busy_timeout=5000",
		"_foreign_keys=ON",
		"_auto_vacuum=INCREMENTAL",
		"_mmap_size=268435456",
		"_locking_mode=NORMAL",
	}
	gormConfig := gorm.Config{Logger: logger.Discard}
	if debug {
		gormConfig.Logger = logger.Default.LogMode(logger.Info)
	}
	db, err = gorm.Open(sqlite.Open(path+"?"+strings.Join(params, "&")), &gormConfig)
	if err != nil {
		return err
	}

	return db.AutoMigrate(
		&model.User{},
		&model.Channel{},
		&model.Group{},
		&model.GroupItem{},
		&model.LLMInfo{},
		&model.APIKey{},
		&model.Setting{},
		&model.StatsTotal{},
		&model.StatsDaily{},
		&model.StatsHourly{},
		&model.StatsModel{},
		&model.StatsChannel{},
	)
}

func Close() error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func GetDB() *gorm.DB {
	return db
}
