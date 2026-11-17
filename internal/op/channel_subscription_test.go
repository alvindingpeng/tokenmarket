package op

import (
	"context"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// TestNormalizeChannelConfigBaseURLChecksDialect 锁定保存侧的地址必填规则(S1)。
//
// 探测侧(ProbeChannelModels)与保存侧(normalizeChannelConfig)是两条独立入口,
// 只改一处会让"能探测但存不下来"或"能存但探测报错", 因此两处都要断言。
func TestNormalizeChannelConfigBaseURLChecksDialect(t *testing.T) {
	tests := []struct {
		name    string
		dialect model.Dialect
		wantErr string
	}{
		{name: "普通渠道缺地址", dialect: model.DialectGeneric, wantErr: "channel base url is required"},
		{name: "Gemini CLI 渠道缺地址", dialect: model.DialectGeminiCLI, wantErr: "channel base url is required"},
		{name: "订阅渠道允许缺地址", dialect: model.DialectSubscription, wantErr: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := normalizeChannelConfig(context.Background(), model.ChannelConfig{
				Name:    "测试渠道",
				Dialect: tt.dialect,
			})
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("订阅渠道应允许空地址, 实际报错: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got err=%v, want contains %q", err, tt.wantErr)
			}
		})
	}
}

// TestDialectSubscribable 只有 subscription 是订阅方言。
//
// 这个判定被探测与保存两处共用, 它是"要不要地址校验"的唯一开关:
// 若它把别的方言也算进来, 那些渠道就会悄悄失去地址必填, 空地址请求极难排查。
func TestDialectSubscribable(t *testing.T) {
	tests := []struct {
		dialect model.Dialect
		want    bool
	}{
		{model.DialectGeneric, false},
		{model.DialectXunfei, false},
		{model.DialectGeminiCLI, false},
		{model.DialectSubscription, true},
		{"", false},
	}
	for _, tt := range tests {
		if got := tt.dialect.Subscribable(); got != tt.want {
			t.Errorf("Dialect(%q).Subscribable()=%v want %v", tt.dialect, got, tt.want)
		}
	}
}
