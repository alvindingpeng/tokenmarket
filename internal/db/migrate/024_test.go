package migrate

import (
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"path/filepath"
	"testing"
)

func TestAddUserTOTPFieldsLegacyAndIdempotence(t *testing.T) {
	d, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "totp.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := d.DB()
	defer sqlDB.Close()
	if err := d.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	if err := d.Exec("INSERT INTO users(id) VALUES (1)").Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := addUserTOTPFields(d); err != nil {
			t.Fatal(err)
		}
	}
	var row struct {
		TOTPEnabled         bool
		TOTPSecretEncrypted string
		TOTPLastStep        int64
		TOTPVersion         int64
		TOTPAttempts        int
		TOTPRecoveryHashes  string
	}
	if err := d.Table("users").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.TOTPEnabled || row.TOTPSecretEncrypted != "" || row.TOTPLastStep != -1 || row.TOTPVersion != 0 || row.TOTPAttempts != 0 || row.TOTPRecoveryHashes != "" {
		t.Fatalf("unsafe defaults: %+v", row)
	}
}
