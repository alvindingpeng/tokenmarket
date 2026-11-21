package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// TestChannelProbeFieldsExposed 自动探测状态必须能在 API 里读到。
//
// 为什么重要: 探测有次数上限(3 次), 到顶后熔断不再自动恢复, 只能人工解锁或等一次成功。
// 而在这次修复之前 probe_count 在后端有列、在前端**零命中**(grep 全无), 管理员看到成员
// 一直红着却分不清"上游坏了"和"自动探测已经放弃它了" —— 两者该采取的动作完全不同。
//
// 这里断言的是"字段存在且可读", 不含前端渲染(前端另派)。
func TestChannelProbeFieldsExposed(t *testing.T) {
	_, _, adminToken, _, _, _, _, _ := ticketAPIEnv(t)
	engine := ticketEngine(t)

	// 走真实创建接口而不是直接插行: 建渠道时缓存由 op 内部重建, /channel/stats 与
	// /channel/detail 读的都是缓存, 绕过它测到的就不是线上那条路径。
	createBody := fmt.Sprintf(`{"name":"probe-fields","enabled":true,"base_url":"https://probe.example.com","dialect":"openai","models":["probe-model"],"keys":[{"name":"probe-key","key":"secret","enabled":true}]}`)
	w0 := ticketAPI(t, engine, http.MethodPost, "/api/v1/channel/create", adminToken, createBody)
	if w0.Code != http.StatusOK {
		t.Fatalf("create channel: %d %s", w0.Code, w0.Body.String())
	}
	var created struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w0.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created channel: %v", err)
	}
	channelID := created.Data.ID
	if channelID == 0 {
		t.Fatalf("创建渠道没拿到 id: %s", w0.Body.String())
	}

	until := time.Now().Add(-time.Minute)
	probedAt := time.Now().Add(-2 * time.Minute)
	// 已探到上限的渠道模型 + 一把同状态的凭据: 两个 DTO 都要外露这三列。
	var channelModel model.ChannelModel
	if err := db.GetDB().Where("channel_id = ? AND name = ?", channelID, "probe-model").First(&channelModel).Error; err != nil {
		t.Fatalf("查已建的渠道模型: %v", err)
	}
	if err := db.GetDB().Model(&model.ChannelModel{}).Where("id = ?", channelModel.ID).Updates(map[string]any{
		"error_count": 2, "breaker_trips": 3, "probe_count": 3, "probe_at": &probedAt,
	}).Error; err != nil {
		t.Fatalf("seed model probe state: %v", err)
	}
	// 凭据同理: 直接改已落库的行, 再让缓存重读, 与"跑了几轮探测之后"的状态一致。
	if err := db.GetDB().Model(&model.ChannelKey{}).
		Where("channel_id = ? AND name = ?", channelID, "probe-key").
		Updates(map[string]any{
			"error_count": 2, "breaker_trips": 3, "disabled_until": &until,
			"probe_count": 3, "probe_at": &probedAt,
		}).Error; err != nil {
		t.Fatalf("seed key probe state: %v", err)
	}
	// 缓存由 op 内部持有, handler 层没有出口可调: 再走一次 update(配置不变),
	// 它会在结束时按库重载该渠道的凭据/模型缓存 —— 与建渠道是同一条重载路径。
	updateBody := fmt.Sprintf(`{"id":%d,"name":"probe-fields","enabled":true,"base_url":"https://probe.example.com","dialect":"openai","models":["probe-model"],"keys":[{"name":"probe-key","key":"secret","enabled":true}]}`, channelID)
	if w := ticketAPI(t, engine, http.MethodPost, "/api/v1/channel/update", adminToken, updateBody); w.Code != http.StatusOK {
		t.Fatalf("update channel: %d %s", w.Code, w.Body.String())
	}

	// 列表响应: 渠道模型统计里要有 probe_count / probe_at / probe_limited。
	w := ticketAPI(t, engine, http.MethodGet, "/api/v1/channel/stats", adminToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("channel stats: %d %s", w.Code, w.Body.String())
	}
	var stats struct {
		Data []struct {
			Models []map[string]any `json:"models"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	found := false
	for _, item := range stats.Data {
		for _, m := range item.Models {
			if m["model_name"] != "probe-model" {
				continue
			}
			found = true
			// JSON 数字解成 float64: 断言存在且值正确, 而不是"字段缺失时零值恰好等于 0"。
			if got, ok := m["probe_count"].(float64); !ok || got != 3 {
				t.Fatalf("probe_count = %v, want 3 (探到上限的次数必须可读)", m["probe_count"])
			}
			if _, ok := m["probe_at"].(string); !ok {
				t.Fatalf("probe_at 缺失或为 null: %v (探过就该给出时间)", m["probe_at"])
			}
			// probe_limited 是后端算好的派生值: 前端不该自己硬编码上限。
			if limited, ok := m["probe_limited"].(bool); !ok || !limited {
				t.Fatalf("probe_limited = %v, want true (probe_count 已达上限)", m["probe_limited"])
			}
		}
	}
	if !found {
		t.Fatalf("渠道列表里没找到 probe-model: %s", w.Body.String())
	}

	// 详情响应: 凭据配置里同样要有这三列(提交侧不写回, 但读要能看到)。
	detailPath := fmt.Sprintf("/api/v1/channel/detail/%d", channelID)
	detailResp := ticketAPI(t, engine, http.MethodGet, detailPath, adminToken, "")
	if detailResp.Code != http.StatusOK {
		t.Fatalf("channel detail: %d %s", detailResp.Code, detailResp.Body.String())
	}
	var detail struct {
		Data struct {
			Keys []map[string]any `json:"keys"`
		} `json:"data"`
	}
	if err := json.Unmarshal(detailResp.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v; body=%s", err, detailResp.Body.String())
	}
	var keyPayload map[string]any
	for _, k := range detail.Data.Keys {
		if k["name"] == "probe-key" {
			keyPayload = k
			break
		}
	}
	if keyPayload == nil {
		t.Fatalf("详情里没找到 probe-key")
	}
	if got, ok := keyPayload["probe_count"].(float64); !ok || got != 3 {
		t.Fatalf("凭据 probe_count = %v, want 3", keyPayload["probe_count"])
	}
	if _, ok := keyPayload["probe_at"].(string); !ok {
		t.Fatalf("凭据 probe_at 缺失: %v", keyPayload["probe_at"])
	}
	if limited, ok := keyPayload["probe_limited"].(bool); !ok || !limited {
		t.Fatalf("凭据 probe_limited = %v, want true", keyPayload["probe_limited"])
	}
}
