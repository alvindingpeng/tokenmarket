package handlers

import (
	"net/http"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	// 站点信息是公开身份文案(站点名/描述/联系方式/公告/维护提示), 未登录的登录页也要读,
	// 因此单开一个不带 Auth 的分组; 只读白名单字段, 绝不整表下发。
	router.NewGroupRouter("/api/v1/site").
		AddRoute(
			router.NewRoute("/config", http.MethodGet).
				Handle(siteConfig),
		)
}

// boolSetting 读取布尔设置, 缺失或值非法时按 false 处理: 站点文案不该因为一个坏值而整段接口失败。
func boolSetting(key model.SettingKey) bool {
	value, err := op.SettingGetBool(key)
	if err != nil {
		return false
	}
	return value
}

// textSetting 读取文本设置, 缺失时返回空串, 由前端决定回退文案。
func textSetting(key model.SettingKey) string {
	value, err := op.SettingGetString(key)
	if err != nil {
		return ""
	}
	return value
}

// siteConfig 下发"系统信息配置"的全部消费字段: 登录页、应用外壳横幅与浏览器标题共用。
func siteConfig(c *gin.Context) {
	// 公告仅在开关打开且有正文时下发, 前端据此决定横幅显隐, 无需再读一次开关。
	announcement := ""
	if boolSetting(model.SettingKeySiteAnnouncementEnabled) {
		announcement = textSetting(model.SettingKeySiteAnnouncement)
	}
	maintenanceNotice := textSetting(model.SettingKeyMaintenanceNotice)
	if maintenanceNotice == "" {
		maintenanceNotice = "service under maintenance"
	}
	resp.Success(c, gin.H{
		"site_name":          textSetting(model.SettingKeySiteName),
		"site_description":   textSetting(model.SettingKeySiteDescription),
		"site_contact":       textSetting(model.SettingKeySiteContact),
		"announcement":       announcement,
		"maintenance_mode":   boolSetting(model.SettingKeyMaintenanceMode),
		"maintenance_notice": maintenanceNotice,
	})
}
