package relay

import (
	"os"
	"strconv"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/conf"
	dbmodel "github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/transformer/model"
	"github.com/gin-gonic/gin"
)

var maxSSEEventSize = 2 * 1024 * 1024

func init() {
	if raw := strings.TrimSpace(os.Getenv(strings.ToUpper(conf.APP_NAME) + "_RELAY_MAX_SSE_EVENT_SIZE")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			maxSSEEventSize = v
		}
	}
}

// hopByHopHeaders 定义不应转发的 HTTP 头
var hopByHopHeaders = map[string]bool{
	"authorization":       true,
	"x-api-key":           true,
	"connection":          true,
	"keep-alive":          true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
	"te":                  true,
	"trailer":             true,
	"transfer-encoding":   true,
	"upgrade":             true,
	"content-length":      true,
	"host":                true,
	"accept-encoding":     true,
}

// relayContext 保存请求转发过程中的上下文信息
type relayContext struct {
	c               *gin.Context
	inAdapter       model.Inbound
	outAdapter      model.Outbound
	internalRequest *model.InternalLLMRequest
	channel         *dbmodel.Channel
	metrics         *RelayMetrics
}
