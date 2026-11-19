package op

import (
	"context"
	"fmt"
	"math"
	"strconv"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/utils/cache"
)

var settingCache = cache.New[model.SettingKey, string](16)

func SettingList(ctx context.Context) ([]model.Setting, error) {
	settings := make([]model.Setting, 0, settingCache.Len())
	for key, value := range settingCache.GetAll() {
		if model.IsInternalSetting(key) {
			continue
		}
		// 密钥类设置项(如 GitHub 令牌)只回显"已配置"占位符: 真值一旦下发到浏览器,
		// 任何一个能打开设置页的会话都能把它读走, 而它对更新检查只是抬高限流而已。
		if model.IsSecretSetting(key) && value != "" {
			value = model.TokenConfiguredMask
		}
		settings = append(settings, model.Setting{
			Key:   key,
			Value: value,
		})
	}
	return settings, nil
}

// SettingUpsert 写入或更新任意键(含内部键); 供启动初始化等内部流程使用。
// 密钥类/内部键落库为信封密文(map 更新由 gorm 回调加密), 运行时缓存与读取仍是明文。
func SettingUpsert(key model.SettingKey, value string) error {
	setting := model.Setting{Key: key, Value: value}
	result := db.GetDB().Model(&setting).
		Where("key = ?", key).
		Updates(map[string]any{"value": value})
	if result.Error != nil {
		return fmt.Errorf("failed to upsert setting: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		// 兜底创建必须用干净的明文结构体: map 更新会把 map 值(已是库内密文形态)回写进上面的
		// setting 结构体, 拿它 Create 会被序列化器二次加密, 解开一层后剩下的是信封而不是秘密。
		if err := db.GetDB().Create(&model.Setting{Key: key, Value: value}).Error; err != nil {
			return fmt.Errorf("failed to create setting: %w", err)
		}
	}
	settingCache.Set(key, value)
	return nil
}

// SettingGetFloat 读取浮点设置值。
func SettingGetFloat(key model.SettingKey) float64 {
	value, err := SettingGetString(key)
	if err != nil {
		return 0
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0
	}
	return parsed
}

func SettingGetString(key model.SettingKey) (string, error) {
	setting, ok := settingCache.Get(key)
	if !ok {
		return "", fmt.Errorf("setting not found")
	}
	return setting, nil
}

func SettingSetString(key model.SettingKey, value string) error {
	valueCache, ok := settingCache.Get(key)
	if !ok {
		return fmt.Errorf("setting not found")
	}
	if valueCache == value {
		return nil
	}
	result := db.GetDB().Model(&model.Setting{Key: key}).Update("Value", value)
	if result.Error != nil {
		return fmt.Errorf("failed to set setting: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("failed to set setting, key not found")
	}
	settingCache.Set(key, value)
	return nil
}

func SettingGetInt(key model.SettingKey) (int, error) {
	setting, ok := settingCache.Get(key)
	if !ok {
		return 0, fmt.Errorf("setting not found")
	}
	return strconv.Atoi(setting)
}

// SettingGetIntDefault 读取整型设置, 缺失或解析失败时返回 fallback, 常用于阈值等可降级选项。
func SettingGetIntDefault(key model.SettingKey, fallback int) int {
	if v, err := SettingGetInt(key); err == nil {
		return v
	}
	return fallback
}

func SettingGetBool(key model.SettingKey) (bool, error) {
	setting, ok := settingCache.Get(key)
	if !ok {
		return false, fmt.Errorf("setting not found")
	}
	return strconv.ParseBool(setting)
}

func SettingSetInt(key model.SettingKey, value int) error {
	valueCache, ok := settingCache.Get(key)
	if !ok {
		return fmt.Errorf("setting not found")
	}
	valueCacheNum, err := strconv.Atoi(valueCache)
	if err != nil {
		return fmt.Errorf("failed to set setting: %w", err)
	}
	if valueCacheNum == value {
		return nil
	}
	result := db.GetDB().Model(&model.Setting{Key: key}).Update("Value", value)
	if result.Error != nil {
		return fmt.Errorf("failed to set setting: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("failed to set setting, key not found")
	}
	settingCache.Set(key, strconv.Itoa(value))
	return nil
}

func settingRefreshCache(ctx context.Context) error {
	db := db.GetDB().WithContext(ctx)

	var settings []model.Setting
	if err := db.Find(&settings).Error; err != nil {
		return fmt.Errorf("failed to get settings: %w", err)
	}

	existingKeys := make(map[model.SettingKey]bool)
	for _, setting := range settings {
		existingKeys[setting.Key] = true
	}

	defaultSettings := model.DefaultSettings()
	missingSettings := make([]model.Setting, 0, len(defaultSettings))

	for _, defaultSetting := range defaultSettings {
		if !existingKeys[defaultSetting.Key] {
			missingSettings = append(missingSettings, defaultSetting)
		}
	}

	if len(missingSettings) > 0 {
		if err := db.CreateInBatches(missingSettings, len(missingSettings)).Error; err != nil {
			return fmt.Errorf("failed to create missing settings: %w", err)
		}
		settings = append(settings, missingSettings...)
	}
	for _, setting := range settings {
		settingCache.Set(setting.Key, setting.Value)
	}
	return nil
}

// AllowPrivateUpstream 返回是否允许上游/出口指向私网地址(SSRF 防护例外)。
// 读取失败按不允许处理: 防护缺失时宁可拒绝内网目标, 也不能静默放开。
func AllowPrivateUpstream() bool {
	allowed, err := SettingGetBool(model.SettingKeyAllowPrivateUpstream)
	return err == nil && allowed
}

// AdminDefaultPasswordChanged 返回默认管理员是否已改过初始密码。
// 读取失败按"未改"处理: 强制改密是保守方向的误判, 放开才是危险方向的误判。
func AdminDefaultPasswordChanged() bool {
	changed, err := SettingGetBool(model.SettingKeyAdminDefaultPwdChanged)
	return err == nil && changed
}
