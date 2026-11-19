package migrate

import (
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"path/filepath"
	"testing"
)

func TestChannelQuotaPauseMigration(t *testing.T) {
	d, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "pause.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := d.DB()
	defer sqlDB.Close()
	if err = d.Exec("CREATE TABLE channels (id INTEGER PRIMARY KEY, enabled BOOLEAN)").Error; err != nil {
		t.Fatal(err)
	}
	if err = d.Exec("INSERT INTO channels VALUES(1,1)").Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = addChannelQuotaPause(d); err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	if err = d.Raw("SELECT count(*) FROM channels WHERE id=1 AND enabled=1 AND disabled_until IS NULL").Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("migration altered existing channel")
	}
}
