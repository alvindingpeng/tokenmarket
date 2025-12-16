package relay

import (
	"io"
	"net/http"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/client"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/relay/balancer"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/transformer/inbound"
	"github.com/alvindingpeng/switchyard/internal/transformer/outbound"
	"github.com/alvindingpeng/switchyard/internal/utils/log"
	"github.com/gin-gonic/gin"
	"github.com/tmaxmax/go-sse"
)

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

func Handler(inboundType inbound.InboundType, c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	inAdapter := inbound.Get(inboundType)

	internalRequest, err := inAdapter.TransformRequest(c.Request.Context(), body)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	if err := internalRequest.Validate(); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	group, err := op.GroupGetMap(internalRequest.Model, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusNotFound, "model not found")
		return
	}
	b := balancer.GetBalancer(group.Mode)
	item := b.Select(group.Items)
	if item == nil {
		resp.Error(c, http.StatusServiceUnavailable, "no available channel")
		return
	}
	stats := NewRelayStats(item.ChannelID, item.ModelName)
	success := false
	defer func() {
		stats.Save(success)
	}()
	for item != nil {
		channel, err := op.ChannelGet(item.ChannelID, c.Request.Context())
		if err != nil {
			log.Warnf("failed to get channel: %v", err)
			item = b.Next(group.Items, item)
			continue
		}
		log.Infof("forwarding to channel: %s model: %s", channel.Name, item.ModelName)
		outAdapter := outbound.Get(channel.Type)
		internalRequest.Model = item.ModelName
		ok := func() bool {
			outboundRequest, err := outAdapter.TransformRequest(c.Request.Context(), internalRequest, channel.BaseURL, channel.Key)
			if err != nil {
				log.Warnf("failed to create request: %v", err)
				return false
			}
			for key, values := range c.Request.Header {
				for _, value := range values {
					if hopByHopHeaders[strings.ToLower(key)] {
						continue
					}
					outboundRequest.Header.Set(key, value)
				}
			}
			httpClient, err := client.GetHTTPClient(channel.Proxy)
			if err != nil {
				log.Warnf("failed to get http client: %v", err)
				return false
			}
			response, err := httpClient.Do(outboundRequest)
			if err != nil {
				log.Warnf("failed to send request: %v", err)
				return false
			}
			defer response.Body.Close()

			if response.StatusCode != http.StatusOK {
				log.Warnf("upstream server error: %d", response.StatusCode)
				body, err := io.ReadAll(response.Body)
				if err != nil {
					log.Warnf("failed to read response body: %v", err)
					return false
				}
				log.Warnf("upstream server error: %s", string(body))
				return false
			}
			if internalRequest.Stream != nil && *internalRequest.Stream {
				c.Header("Content-Type", "text/event-stream")
				c.Header("Cache-Control", "no-cache")
				c.Header("Connection", "keep-alive")
				c.Header("X-Accel-Buffering", "no")
				ctx := c.Request.Context()
				for ev, err := range sse.Read(response.Body, nil) {
					select {
					case <-ctx.Done():
						log.Infof("client disconnected, stopping stream")
						return true
					default:
					}
					if err != nil {
						log.Warnf("failed to read event: %v", err)
						break
					}
					internalStream, err := outAdapter.TransformStream(ctx, []byte(ev.Data))
					if err != nil {
						log.Warnf("failed to transform stream: %v", err)
						continue
					}
					if internalStream == nil {
						continue
					}
					if internalStream.Usage != nil {
						stats.UpdateUsage(*internalStream.Usage)
					}
					inStream, err := inAdapter.TransformStream(ctx, internalStream)
					if err != nil {
						log.Warnf("failed to transform stream: %v", err)
						continue
					}
					if len(inStream) == 0 {
						continue
					}
					c.Writer.Write(inStream)
					c.Writer.Flush()
				}
				log.Infof("stream end")
				return true
			}
			internalResponse, err := outAdapter.TransformResponse(c.Request.Context(), response)
			if err != nil {
				log.Warnf("failed to transform response: %v", err)
				return false
			}
			if internalResponse.Usage != nil {
				stats.UpdateUsage(*internalResponse.Usage)
			}
			inResponse, err := inAdapter.TransformResponse(c.Request.Context(), internalResponse)
			if err != nil {
				log.Warnf("failed to transform response: %v", err)
				return false
			}
			c.Data(http.StatusOK, "application/json", inResponse)
			return true
		}()

		if ok {
			success = true
			return
		}
		item = b.Next(group.Items, item)
	}
	resp.Error(c, http.StatusBadGateway, "all channels failed")
}
