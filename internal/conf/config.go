package conf

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/spf13/viper"
)

// 请求体上限的缺省值: 网关会把请求体读进内存做改写与留痕, 不设上限时一个大 body
// 就足以把内存吃穿; 正常文本补全请求远小于 32 MiB。生图底图与生视频素材是真正的
// 例外, 故转发接口(/v1)单列一档而不是直接豁免。
const (
	DefaultMaxBodyBytes      int64 = 32 << 20
	DefaultRelayMaxBodyBytes int64 = 512 << 20
)

// MaxBodyBytes 返回管理端请求体上限; 配置为 0 或负数时回退缺省值。
func MaxBodyBytes() int64 {
	if AppConfig.Server.MaxBodyBytes > 0 {
		return AppConfig.Server.MaxBodyBytes
	}
	return DefaultMaxBodyBytes
}

// RelayMaxBodyBytes 返回转发接口(/v1)的请求体上限。
func RelayMaxBodyBytes() int64 {
	if AppConfig.Server.RelayMaxBodyBytes > 0 {
		return AppConfig.Server.RelayMaxBodyBytes
	}
	return DefaultRelayMaxBodyBytes
}

type Server struct {
	Host           string   `mapstructure:"host"`
	Port           int      `mapstructure:"port"`
	TrustedProxies []string `mapstructure:"trusted_proxies"`
	// MaxBodyBytes 管理端请求体上限(字节), 缺省 32 MiB; <=0 用内置默认值。
	// 网关会把请求体读进内存做改写与留痕, 不设上限时一个大 body 就能把内存吃穿。
	MaxBodyBytes int64 `mapstructure:"max_body_bytes"`
	// RelayMaxBodyBytes 转发接口(/v1)的请求体上限; 生图底图与生视频素材需要更宽的一档,
	// 但仍是上限而不是豁免。缺省 512 MiB。
	RelayMaxBodyBytes int64 `mapstructure:"relay_max_body_bytes"`
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
	// 缺省值与下方常量保持一致。
	viper.SetDefault("server.max_body_bytes", DefaultMaxBodyBytes)
	viper.SetDefault("server.relay_max_body_bytes", DefaultRelayMaxBodyBytes)
	viper.SetDefault("database.type", "sqlite")
	viper.SetDefault("database.path", "data/data.db")
	viper.SetDefault("log.level", "info")
}
