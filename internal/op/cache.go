package op

import (
	"context"
	"fmt"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
)

func InitCache() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := settingRefreshCache(ctx); err != nil {
		return fmt.Errorf("setting refresh cache error: %v", err)
	}
	if err := channelRefreshCache(ctx); err != nil {
		return fmt.Errorf("channel refresh cache error: %v", err)
	}
	if err := groupRefreshCache(ctx); err != nil {
		return fmt.Errorf("group refresh cache error: %v", err)
	}
	if err := apiKeyRefreshCache(ctx); err != nil {
		return fmt.Errorf("api key refresh cache error: %v", err)
	}
	if err := llmRefreshCache(ctx); err != nil {
		return fmt.Errorf("llm refresh cache error: %v", err)
	}
	if err := autoAddRuleRefreshCache(ctx); err != nil {
		return fmt.Errorf("model auto-add rule refresh cache error: %v", err)
	}
	if err := capabilityRefreshCache(ctx); err != nil {
		return fmt.Errorf("model capability refresh cache error: %v", err)
	}
	if err := modelReferenceRefreshCache(ctx); err != nil {
		return fmt.Errorf("model reference refresh cache error: %w", err)
	}
	if err := statsRefreshCache(ctx); err != nil {
		return fmt.Errorf("stats refresh cache error: %v", err)
	}

	// 注入质量权重读取函数（供 model.QualityGradeOf 跨包读取设置）。
	model.QualityWeightGetter = SettingGetIntDefault
	// 注入 Gemini OAuth 设置读取函数(供 model.geminiOAuthClientID/Secret 读取设置库)。
	// 读取失败/为空时 model 层会回退到环境变量与内置默认值。
	model.GeminiOAuthSettingGetter = SettingGetString

	return nil
}

func SaveCache() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := StatsSaveDB(ctx); err != nil {
		return err
	}
	return nil
}
