package conf

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/spf13/viper"
)

type Server struct {
	Host           string   `mapstructure:"host"`
	Port           int      `mapstructure:"port"`
	TrustedProxies []string `mapstructure:"trusted_proxies"`
}

type Log struct {
	Level string `mapstructure:"level"`
}

type Database struct {
	Type string `mapstructure:"type"`
	Path string `mapstructure:"path"`
}

// Backup 备份机密配置: 只走 config.json 或环境变量(OCTOPUS_BACKUP_*), 不入库。
// 备份文件包含整个数据库, 口令与异地凭据若入库会被一并备份出去。
type Backup struct {
	Passphrase     string `mapstructure:"passphrase"`      // 备份加密口令, 空表示不加密。
	RemoteURL      string `mapstructure:"remote_url"`      // WebDAV 异地目录地址, 空表示不推送。
	RemoteUser     string `mapstructure:"remote_user"`     // WebDAV 用户名(可选)。
	RemotePassword string `mapstructure:"remote_password"` // WebDAV 密码(可选)。
}

type Config struct {
	Server   Server   `mapstructure:"server"`
	Log      Log      `mapstructure:"log"`
	Database Database `mapstructure:"database"`
	Backup   Backup   `mapstructure:"backup"`
}

var AppConfig Config

func Load(path string) error {
	if path != "" {
		viper.SetConfigFile(path)
	} else {
		viper.SetConfigName("config")
		viper.SetConfigType("json")
		viper.AddConfigPath("data")
	}

	viper.AutomaticEnv()
	viper.SetEnvPrefix(APP_NAME)
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	setDefaults()

	if err := viper.ReadInConfig(); err == nil {
		log.Infof("Using config file: %s", viper.ConfigFileUsed())
	} else {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			log.Infof("Config file not found, creating default config")
			if err := os.MkdirAll("data", 0755); err != nil {
				log.Errorf("Failed to create data directory: %v", err)
			}
			if err := viper.SafeWriteConfigAs("data/config.json"); err != nil {
				log.Errorf("Failed to create default config: %v", err)
			}
		} else {
			return fmt.Errorf("error reading config file: %w", err)
		}
	}

	if err := viper.Unmarshal(&AppConfig); err != nil {
		return fmt.Errorf("unable to decode config into struct: %w", err)
	}
	return nil
}

func setDefaults() {
	viper.SetDefault("server.host", "0.0.0.0")
	viper.SetDefault("server.port", 8080)
	viper.SetDefault("database.type", "sqlite")
	viper.SetDefault("database.path", "data/data.db")
	viper.SetDefault("log.level", "info")
}
