package cmd

import (
	"context"
	"os"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/conf"
	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/secretstore"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
)

// 静态凭据信封加密的运维入口: 主密钥(KEK)装配与既有明文迁移。
//
// 主密钥来源(优先级从高到低, 都没有默认值):
//
//	SWITCHYARD_SECRET_KEK         环境变量直接给密钥材料(hex 或 base64, 32 字节)
//	SWITCHYARD_SECRET_KEK_FILE    环境变量给密钥文件路径
//	SWITCHYARD_KEK_FILE           同上别名(部署脚本用这个名字也可以)
//	config.json secret.kek_file   配置文件给密钥文件路径
//
// 可选 SWITCHYARD_SECRET_KEY_ID / config.json secret.key_id 显式指定信封密钥 ID;
// 留空按密钥材料派生, 共享同一 KEK 的多实例自动一致。
var secretCmd = &cobra.Command{
	Use:   "secret",
	Short: "Static secret envelope encryption utilities (migrate / status)",
}

// secretDBOverride 是 --database: 指向隔离克隆件做部署前演练(不碰生产库)。
// 不给时按 config 的 database.path(即生产库), 这是正式迁移的正常路径;
// 给了则必须通过 verify-migration 的同一安全契约(只接受克隆件, 拒绝生产数据目录/软链/硬链)。
var secretDBOverride string

// resolveSecretDBPath 决定 secret 命令操作的库。
func resolveSecretDBPath() (string, error) {
	if strings.TrimSpace(secretDBOverride) == "" {
		return conf.AppConfig.Database.Path, nil
	}
	return validateMigrationVerifyTarget(secretDBOverride)
}

var secretMigrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Encrypt existing plaintext secrets in place (atomic, idempotent)",
	Long: `把 settings / channel_keys 里遗留的明文凭据就地包进信封密文。

幂等: 已加密的行只验不改(顺带确认当前主密钥能打开它们), 重复执行零写入。
原子: 全部写入在一个事务里, 中途失败不留半加密状态。
fail closed: 主密钥缺失/非法、或存量信封打不开(主密钥配错), 一律报错退出。

建议在服务停止时执行(切换版本的维护窗口内跑一次); 即使不跑, start 启动时也会自动执行同一迁移。`,
	PreRunE: func(cmd *cobra.Command, args []string) error {
		return conf.Load(cfgFile)
	},
	Run: func(cmd *cobra.Command, args []string) {
		if err := initSecretStore(); err != nil {
			log.Errorf("secret master key init error: %v", err)
			os.Exit(1)
		}
		dbPath, err := resolveSecretDBPath()
		if err != nil {
			log.Errorf("secret migration target: %v", err)
			os.Exit(1)
		}
		log.Infof("secret migration target: type=%s path=%s", conf.AppConfig.Database.Type, dbPath)
		if err := db.InitDB(conf.AppConfig.Database.Type, dbPath, conf.IsDebug()); err != nil {
			log.Errorf("database init error: %v", err)
			os.Exit(1)
		}
		defer db.Close()
		stats, err := db.MigrateSecretRows(context.Background(), false)
		if err != nil {
			log.Errorf("secret migration error: %v", err)
			os.Exit(1)
		}
		log.Infof("secret migration done: settings_encrypted=%d channel_keys_encrypted=%d already_encrypted=%d empty_skipped=%d",
			stats.SettingsEncrypted, stats.ChannelKeysEncrypted, stats.AlreadyEncrypted, stats.EmptySkipped)
	},
}

var secretStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Report secret at-rest coverage without writing (dry run)",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		return conf.Load(cfgFile)
	},
	Run: func(cmd *cobra.Command, args []string) {
		if err := initSecretStore(); err != nil {
			log.Errorf("secret master key init error: %v", err)
			os.Exit(1)
		}
		dbPath, err := resolveSecretDBPath()
		if err != nil {
			log.Errorf("secret status target: %v", err)
			os.Exit(1)
		}
		log.Infof("secret status target: type=%s path=%s", conf.AppConfig.Database.Type, dbPath)
		if err := db.InitReadOnlySecretDB(conf.AppConfig.Database.Type, dbPath); err != nil {
			log.Errorf("database init error: %v", err)
			os.Exit(1)
		}
		defer db.Close()
		stats, err := db.MigrateSecretRows(context.Background(), true)
		if err != nil {
			log.Errorf("secret status error: %v", err)
			os.Exit(1)
		}
		log.Infof("secret status (key ID %s): would_encrypt=%d already_encrypted=%d empty_skipped=%d",
			secretstore.Active().KeyID(), stats.Total(), stats.AlreadyEncrypted, stats.EmptySkipped)
	},
}

// initSecretStore 从外部来源装配主密钥; 任何失败都返回错误, 调用方必须拒绝继续(fail closed)。
func initSecretStore() error {
	if err := secretstore.Init(secretKEKConfig()); err != nil {
		return err
	}
	log.Infof("secret master key ready (key ID %s)", secretstore.Active().KeyID())
	return nil
}

// secretKEKConfig 收集主密钥来源: 环境变量优先(systemd 里最省事), 其次 config.json。
func secretKEKConfig() secretstore.KEKConfig {
	return secretstore.KEKConfig{
		Inline: strings.TrimSpace(os.Getenv("SWITCHYARD_SECRET_KEK")),
		File:   firstNonEmpty(os.Getenv("SWITCHYARD_SECRET_KEK_FILE"), os.Getenv("SWITCHYARD_KEK_FILE"), conf.AppConfig.Secret.KEKFile),
		KeyID:  firstNonEmpty(os.Getenv("SWITCHYARD_SECRET_KEY_ID"), conf.AppConfig.Secret.KeyID),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func init() {
	secretCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is ./data/config.json)")
	secretCmd.PersistentFlags().StringVar(&secretDBOverride, "database", "",
		"rehearse against an isolated SQLite clone instead of config database.path (production data paths are refused)")
	secretCmd.AddCommand(secretMigrateCmd, secretStatusCmd)
	rootCmd.AddCommand(secretCmd)
}
