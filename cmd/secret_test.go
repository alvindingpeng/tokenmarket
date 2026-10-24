package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/conf"
)

func testEnv(t *testing.T, key, value string) {
	t.Helper()
	// 空串与未设置在 secretKEKConfig 里等价(都走 firstNonEmpty 跳过), t.Setenv 即可。
	t.Setenv(key, value)
}

// TestResolveSecretDBPath 锁定演练契约: 不给 --database 时操作 config 的库(生产, 正式迁移路径);
// 给了 --database 只接受隔离克隆件(与 verify-migration 同一安全契约, 拒绝生产数据目录)。
func TestResolveSecretDBPath(t *testing.T) {
	previous := conf.AppConfig
	t.Cleanup(func() { conf.AppConfig = previous })
	conf.AppConfig.Database.Path = "/opt/switchyard/data/data.db"

	secretDBOverride = ""
	t.Cleanup(func() { secretDBOverride = "" })
	if got, err := resolveSecretDBPath(); err != nil || got != "/opt/switchyard/data/data.db" {
		t.Fatalf("default target = %q, %v; want config database.path", got, err)
	}

	secretDBOverride = "/opt/switchyard/data/data.db"
	if _, err := resolveSecretDBPath(); err == nil {
		t.Fatal("--database accepted the protected production path, want refusal")
	}
	secretDBOverride = "/opt/switchyard/data/copy.db"
	if _, err := resolveSecretDBPath(); err == nil {
		t.Fatal("--database accepted a path under the protected production data dir, want refusal")
	}

	clone := filepath.Join(t.TempDir(), "clone.db")
	header := append([]byte("SQLite format 3\x00"), make([]byte, 32)...)
	if err := os.WriteFile(clone, header, 0600); err != nil {
		t.Fatal(err)
	}
	secretDBOverride = clone
	if got, err := resolveSecretDBPath(); err != nil {
		t.Fatalf("--database refused an isolated clone: %v", err)
	} else if got == "" {
		t.Fatal("clone target resolved to empty path")
	}
}

// TestSecretKEKConfigPrecedence 锁定部署契约: 内联 > SECRET_KEK_FILE > KEK_FILE 别名 > config。
func TestSecretKEKConfigPrecedence(t *testing.T) {
	previous := conf.AppConfig
	t.Cleanup(func() { conf.AppConfig = previous })

	writeKeyFile := func(name string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte("00"), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	secretFile := writeKeyFile("secret-kek")
	aliasFile := writeKeyFile("master.key")
	configFile := writeKeyFile("config-kek")

	conf.AppConfig.Secret.KEKFile = ""
	testEnv(t, "SWITCHYARD_SECRET_KEK", "")
	testEnv(t, "SWITCHYARD_SECRET_KEK_FILE", "")
	testEnv(t, "SWITCHYARD_KEK_FILE", "")

	// 只有 config: 用 config。
	conf.AppConfig.Secret.KEKFile = configFile
	if cfg := secretKEKConfig(); cfg.File != configFile {
		t.Fatalf("config fallback = %q, want %q", cfg.File, configFile)
	}

	// 别名环境变量优先于 config。
	testEnv(t, "SWITCHYARD_KEK_FILE", aliasFile)
	if cfg := secretKEKConfig(); cfg.File != aliasFile {
		t.Fatalf("SWITCHYARD_KEK_FILE alias = %q, want %q", cfg.File, aliasFile)
	}

	// SECRET_KEK_FILE 优先于别名。
	testEnv(t, "SWITCHYARD_SECRET_KEK_FILE", secretFile)
	if cfg := secretKEKConfig(); cfg.File != secretFile {
		t.Fatalf("SWITCHYARD_SECRET_KEK_FILE = %q, want %q", cfg.File, secretFile)
	}

	// 内联材料优先于一切文件。
	testEnv(t, "SWITCHYARD_SECRET_KEK", "00")
	cfg := secretKEKConfig()
	if cfg.Inline != "00" || cfg.File != secretFile {
		t.Fatalf("inline precedence: %+v", cfg)
	}
}
