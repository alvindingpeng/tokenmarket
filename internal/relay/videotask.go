package relay

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
)

// ForwardVideoTask 处理生视频任务的查询(GET)与取消(DELETE): /v1/videos/{id}。
// 这两个请求不带模型名, 无法按模型解析分组, 因此按创建时落库的任务归属直接定位当时的渠道与凭据。
// 与创建不同, 查询与取消都不计费: 轮询是客户端取回结果的必要动作, 按次收费等于让用户为等待付费。
func ForwardVideoTask(deleteTask bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		jobID := strings.TrimSpace(c.Param("id"))
		if jobID == "" {
			rejectVideoTask(c, http.StatusBadRequest, "video task id is required")
			return
		}
		job, err := op.VideoJobGet(jobID, uint(c.GetInt("api_key_user_id")))
		if err != nil {
			rejectVideoTask(c, http.StatusNotFound, "video task not found")
			return
		}
		request, channel, err := buildVideoTaskRequest(c.Request.Context(), job, jobID, deleteTask)
		if err != nil {
			rejectVideoTask(c, http.StatusServiceUnavailable, err.Error())
			return
		}
		// 视频任务同样走出口池: 它是一次普通的上游调用, 没有理由绕过出口择优。
		egress, _, egressErr := pickEgress(channel, "")
		if egressErr != nil {
			rejectVideoTask(c, http.StatusBadGateway, egressErr.Error())
			return
		}
		httpClient, err := resolveUpstreamClient(channel, egress)
		if err != nil {
			rejectVideoTask(c, http.StatusBadGateway, err.Error())
			return
		}
		response, err := httpclient.NewHttpClientWithClient(httpClient).Do(c.Request.Context(), request)
		if err != nil {
			rejectVideoTask(c, http.StatusBadGateway, err.Error())
			return
		}
		// 上游已确认删除时同步清掉本地归属, 否则会留下一条再也查不到的任务记录。
		if deleteTask && response.StatusCode < http.StatusBadRequest {
			if err := op.VideoJobDelete(jobID); err != nil {
				log.Warnf("failed to delete video job %s: %v", jobID, err)
			}
		}
		c.Data(response.StatusCode, response.Headers.Get("Content-Type"), response.Body)
	}
}

// buildVideoTaskRequest 按任务归属还原创建时的渠道授权, 并据此构造查询或取消请求。
// 归属里的授权, 凭据或渠道已被删除时无法定位上游, 直接返回错误而不是退到别的成员:
// 任务 ID 只对创建它的那家上游有效, 换个渠道查必然查不到, 只会把 404 伪装成上游的答复。
func buildVideoTaskRequest(ctx context.Context, job model.VideoJob, jobID string, deleteTask bool) (*httpclient.Request, model.Channel, error) {
	grant, err := op.ChannelGrantGet(job.ChannelGrantID)
	if err != nil {
		return nil, model.Channel{}, errors.New("video task upstream is no longer available")
	}
	if grant.ChannelKey == nil || !grant.ChannelKey.Enabled {
		return nil, model.Channel{}, errors.New("video task upstream is no longer available")
	}
	channel, err := op.ChannelGet(grant.ChannelModel.ChannelID)
	if err != nil {
		return nil, model.Channel{}, errors.New("video task upstream is no longer available")
	}
	outbound, _, _, err := buildOutbound(channel, grant, *grant.ChannelKey, model.ProtocolOpenAIVideo, llm.APIFormatOpenAIVideo)
	if err != nil {
		return nil, model.Channel{}, errors.New("video task upstream is no longer available")
	}
	taskOutbound, ok := outbound.(transformer.VideoTaskOutbound)
	if !ok {
		return nil, model.Channel{}, errors.New("video task query is not supported by this upstream")
	}
	if deleteTask {
		request, err := taskOutbound.BuildDeleteVideoTaskRequest(ctx, jobID)
		return request, channel, err
	}
	request, err := taskOutbound.BuildGetVideoTaskRequest(ctx, jobID)
	return request, channel, err
}

// rejectVideoTask 以 OpenAI 错误形状回复任务查询与取消的失败, 与转发路径的错误体保持一致。
func rejectVideoTask(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"error": gin.H{"message": message, "type": "invalid_request_error"}})
}
