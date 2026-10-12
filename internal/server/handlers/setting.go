package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/bestruirui/octopus/internal/task"
	"github.com/bestruirui/octopus/internal/update"
	"github.com/gin-gonic/gin"
)

func init() {
	// 系统设置与备份属管理后台, 仅管理员可用。
	router.NewGroupRouter("/api/v1/setting").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(getSettingList),
		).
		AddRoute(
			router.NewRoute("/set", http.MethodPost).
				Use(middleware.RequireJSON()).
				Allow(model.RoleAdmin).
				Handle(setSetting),
		).
		AddRoute(
			router.NewRoute("/export", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(exportDB),
		).
		AddRoute(
			router.NewRoute("/import", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(importDB),
		)
}

func getSettingList(c *gin.Context) {
	settings, err := op.SettingList(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, settings)
}

func setSetting(c *gin.Context) {
	var setting model.Setting
	if err := c.ShouldBindJSON(&setting); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := setting.Validate(); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	// 界面拿到的是占位符而不是真值; 原样提交占位符表示"没改", 不能把掩码写进库。
	if model.IsSecretSetting(setting.Key) && setting.Value == model.TokenConfiguredMask {
		audit(c, "setting.set", string(setting.Key), "")
		resp.Success(c, model.Setting{Key: setting.Key, Value: model.TokenConfiguredMask})
		return
	}
	if err := op.SettingSetString(setting.Key, setting.Value); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	// 切到 bearer 时确保已有令牌: 否则开启鉴权的同时把抓取端挡在门外, 管理员无从获取凭据。
	if setting.Key == model.SettingKeyMetricsAuth && setting.Value == "bearer" {
		token, err := op.MetricsTokenEnsure()
		if err != nil {
			resp.Error(c, http.StatusInternalServerError, err.Error())
			return
		}
		audit(c, "setting.set", string(setting.Key), auditSettingValue(setting))
		resp.Success(c, gin.H{"key": setting.Key, "value": setting.Value, "metrics_token": token})
		return
	}
	// 周期类设置改完立刻重算对应任务的定时器。改造前只有 model_info_update_interval
	// 会做这件事, 其余(统计落库、自动备份、健康探测、更新检查)要重启进程才生效。
	if err := task.ApplySetting(setting.Key); err != nil {
		if errors.Is(err, task.ErrNotRegistered) {
			// 该设置没有挂任务, 属正常路径。
		} else {
			// 挂了任务但应用失败要如实回报: 静默返回成功会让管理员以为新周期已经生效。
			audit(c, "setting.apply_failed", string(setting.Key), err.Error())
			resp.Error(c, http.StatusInternalServerError, "saved but not applied: "+err.Error())
			return
		}
	}
	switch setting.Key {
	case model.SettingKeyGithubToken:
		// 令牌直接决定限流额度, 改完必须让下一次读取重新问一次 GitHub,
		// 否则界面还会拿限流期间那份失败结论糊弄管理员。
		update.Invalidate()
	}
	audit(c, "setting.set", string(setting.Key), auditSettingValue(setting))
	resp.Success(c, setting)
}

// auditSettingValue 供审计记录使用: 密钥类设置项(令牌/密码)绝不写原文。
// 审计日志是长期留存且随备份外传的, 一旦写入令牌等于把凭据铺开到备份副本里。
func auditSettingValue(setting model.Setting) string {
	if model.IsSecretSetting(setting.Key) {
		return model.TokenConfiguredMask
	}
	return setting.Value
}

func exportDB(c *gin.Context) {
	dump, err := op.DBExportAll(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	audit(c, "backup.export", "", "")
	c.Header("Content-Type", "application/json")
	c.Header("Content-Disposition", "attachment; filename=\"octopus-export-"+time.Now().Format("20060102150405")+".json\"")
	c.JSON(http.StatusOK, dump)
}

func importDB(c *gin.Context) {
	var dump model.DBDump

	contentType := c.GetHeader("Content-Type")
	if strings.Contains(contentType, "multipart/form-data") {
		fh, err := c.FormFile("file")
		if err != nil {
			resp.Error(c, http.StatusBadRequest, "missing upload file field 'file'")
			return
		}
		f, err := fh.Open()
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		defer f.Close()
		body, err := io.ReadAll(f)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		if err := decodeDBDump(body, &dump); err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
	} else {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		if err := decodeDBDump(body, &dump); err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
	}

	seenLLMNames := make(map[string]struct{}, len(dump.LLMInfos))
	for i := range dump.LLMInfos {
		dump.LLMInfos[i].Name = strings.ToLower(strings.TrimSpace(dump.LLMInfos[i].Name))
		if dump.LLMInfos[i].Name == "" {
			resp.Error(c, http.StatusBadRequest, "model price name cannot be empty")
			return
		}
		if _, ok := seenLLMNames[dump.LLMInfos[i].Name]; ok {
			resp.Error(c, http.StatusBadRequest, "duplicate model price: "+dump.LLMInfos[i].Name)
			return
		}
		seenLLMNames[dump.LLMInfos[i].Name] = struct{}{}
	}
	for i := range dump.Groups {
		if dump.Groups[i].Mode == "" {
			dump.Groups[i].Mode = model.GroupModeManual
		}
		model.NormalizeGroupRelayConfigForMode(&dump.Groups[i].RelayConfig, dump.Groups[i].Mode)
		if !model.IsValidGroupMode(dump.Groups[i].Mode) {
			resp.Error(c, http.StatusBadRequest, "invalid group relay mode")
			return
		}
	}

	result, err := op.DBImportIncremental(c.Request.Context(), &dump)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	if err := op.InitCache(); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	audit(c, "backup.import", "", fmt.Sprintf("channels=%d groups=%d models=%d apikeys=%d", len(dump.Channels), len(dump.Groups), len(dump.LLMInfos), len(dump.APIKeys)))
	resp.Success(c, result)
}

func decodeDBDump(body []byte, dump *model.DBDump) error {
	if dump == nil {
		return json.Unmarshal(body, &struct{}{})
	}

	if err := json.Unmarshal(body, dump); err != nil {
		return err
	}

	if dump.Version == 0 &&
		len(dump.Channels) == 0 &&
		len(dump.Groups) == 0 &&
		len(dump.ChannelModels) == 0 &&
		len(dump.GroupItems) == 0 &&
		len(dump.Settings) == 0 &&
		len(dump.APIKeys) == 0 &&
		len(dump.LLMInfos) == 0 &&
		len(dump.StatsDaily) == 0 &&
		len(dump.StatsHourly) == 0 &&
		len(dump.StatsTotal) == 0 &&
		len(dump.StatsAPIKey) == 0 {
		var wrapper struct {
			Code    int             `json:"code"`
			Message string          `json:"message"`
			Data    json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(body, &wrapper); err == nil && len(wrapper.Data) > 0 {
			return json.Unmarshal(wrapper.Data, dump)
		}
	}

	return nil
}
