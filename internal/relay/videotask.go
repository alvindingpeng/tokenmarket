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
		if !safeVideoTaskID(jobID) {
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
			// Do not expose the upstream URL or transport internals to callers.
			rejectVideoTask(c, http.StatusBadGateway, "video task upstream request failed")
			return
		}
		// 上游已确认删除时同步清掉本地归属, 否则会留下一条再也查不到的任务记录。
		if deleteTask && response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
			if err := op.VideoJobDelete(jobID); err != nil {
				log.Warnf("failed to delete video job %s: %v", jobID, err)
			}
		}
		c.Data(response.StatusCode, response.Headers.Get("Content-Type"), response.Body)
	}
}

// buildVideoTaskRequest 按任务归属还原创建时的渠道授权与凭据, 并据此构造查询或取消请求。
// 归属里的授权、渠道或凭据已无法还原时直接返回错误, 而不是退到别的授权或别的凭据:
// 任务 ID 只对创建它的那家上游有效, 换个渠道或换把凭据查必然查不到, 只会把 404 伪装成上游的答复。
func buildVideoTaskRequest(ctx context.Context, job model.VideoJob, jobID string, deleteTask bool) (*httpclient.Request, model.Channel, error) {
	grant, err := op.ChannelGrantLookup(job.ChannelGrantID)
	if err != nil {
		// Grant rows are routinely removed when an admin edits the grant matrix, while the
		// creation credential can remain valid. Recover the exact channel from that
		// credential; never substitute another credential or channel. Legacy rows without
		// ChannelKeyID cannot be recovered and retain the old 503 behavior.
		if job.ChannelKeyID <= 0 || job.ChannelID <= 0 {
			return nil, model.Channel{}, errors.New("video task upstream is no longer available")
		}
		key, keyErr := op.ChannelKeyLookup(job.ChannelKeyID)
		if keyErr != nil || key.ChannelID != job.ChannelID {
			return nil, model.Channel{}, errors.New("video task upstream is no longer available")
		}
		channel, channelErr := op.ChannelGet(job.ChannelID)
		if channelErr != nil {
			return nil, model.Channel{}, errors.New("video task upstream is no longer available")
		}
		grant = model.ChannelGrant{ID: job.ChannelGrantID, ChannelKeyID: job.ChannelKeyID, Protocols: model.ProtocolOpenAIVideo}
		grant.ChannelModel = &model.ChannelModel{ChannelID: key.ChannelID, Name: job.ModelName}
		grant.ChannelKey = &key
		return buildVideoTaskRequestWithGrant(ctx, job, jobID, deleteTask, grant, channel)
	}
	channel, err := op.ChannelGet(grant.ChannelModel.ChannelID)
	if err != nil || (job.ChannelID > 0 && grant.ChannelModel.ChannelID != job.ChannelID) {
		return nil, model.Channel{}, errors.New("video task upstream is no longer available")
	}
	return buildVideoTaskRequestWithGrant(ctx, job, jobID, deleteTask, grant, channel)
}

func buildVideoTaskRequestWithGrant(ctx context.Context, job model.VideoJob, jobID string, deleteTask bool, grant model.ChannelGrant, channel model.Channel) (*httpclient.Request, model.Channel, error) {
	channelKey, err := resolveVideoTaskKey(job, grant)
	if err != nil {
		return nil, model.Channel{}, err
	}
	outbound, _, _, err := buildOutbound(channel, grant, *channelKey, model.ProtocolOpenAIVideo, llm.APIFormatOpenAIVideo)
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

// resolveVideoTaskKey 找回创建该任务时实际使用的渠道凭据。
//
// 任务只在「创建它的那一把凭据」名下可见, 而渠道凭据会轮换: 换绑授权、停用旧凭据、按策略挑选都会让
// "当前授权绑定的凭据"不再是创建时那把, 拿它去查必然 404。因此优先用任务记录里的创建凭据 ID
// (落库时只记 ID, 不记凭据明文); 创建凭据即使已被停用也照常用它 —— 停用只影响新请求选路,
// 任务仍在这把凭据名下。老记录没有凭据 ID 时才回落授权绑定的凭据(旧行为)。
// 别的凭据一律不顶替: 换一把凭据查不到就是查不到, 硬凑一把只会给出错误答案。
func resolveVideoTaskKey(job model.VideoJob, grant model.ChannelGrant) (*model.ChannelKey, error) {
	if job.ChannelKeyID > 0 {
		if job.ChannelID > 0 && grant.ChannelModel.ChannelID != job.ChannelID {
			return nil, errors.New("video task upstream is no longer available")
		}
		for _, key := range op.ChannelKeysByChannel(grant.ChannelModel.ChannelID) {
			if key.ID != job.ChannelKeyID {
				continue
			}
			matched := key
			return &matched, nil
		}
		// 记录的创建凭据已不存在(被删除或换了渠道), 没有凭据能代表那家上游, 不能退到别的凭据。
		return nil, errors.New("video task upstream is no longer available")
	}
	if grant.ChannelKey != nil && grant.ChannelKey.Enabled {
		return grant.ChannelKey, nil
	}
	return nil, errors.New("video task upstream is no longer available")
}

func safeVideoTaskID(id string) bool {
	if id == "" {
		return false
	}
	for _, ch := range id {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-' {
			continue
		}
		return false
	}
	return true
}

// rejectVideoTask 以 OpenAI 错误形状回复任务查询与取消的失败, 与转发路径的错误体保持一致。
func rejectVideoTask(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"error": gin.H{"message": message, "type": "invalid_request_error"}})
}
