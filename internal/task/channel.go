package task

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/client"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/utils/log"
)

func ChannelBaseUrlDelayTask() {
	channels, err := op.ChannelList(context.Background())
	if err != nil {
		log.Errorf("failed to list channels: %v", err)
		return
	}

	for _, channel := range channels {
		newBaseUrls := make([]model.BaseUrl, 0, len(channel.BaseUrls))
		for _, baseUrl := range channel.BaseUrls {
			var httpClient *http.Client
			var err error
			if !channel.Proxy {
				httpClient, err = client.GetHTTPClientSystemProxy(false)
			} else if channel.ChannelProxy == nil || strings.TrimSpace(*channel.ChannelProxy) == "" {
				httpClient, err = client.GetHTTPClientSystemProxy(true)
			} else {
				httpClient, err = client.GetHTTPClientCustomProxy(strings.TrimSpace(*channel.ChannelProxy))
			}
			if err != nil {
				log.Warnf("failed to build http client for delay test (channel=%d): %v", channel.ID, err)
				httpClient, _ = client.GetHTTPClientSystemProxy(false)
			}
			if httpClient == nil {
				continue
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			start := time.Now()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseUrl.URL, nil)
			req.Header.Set("Range", "bytes=0-0")
			resp, err := httpClient.Do(req)
			cancel()
			if err != nil {
				log.Debugf("delay test failed to request %s (channel=%d): %v", baseUrl.URL, channel.ID, err)
				continue
			}
			resp.Body.Close()
			duration := time.Since(start)
			newBaseUrls = append(newBaseUrls, model.BaseUrl{
				URL:   baseUrl.URL,
				Delay: int(duration.Milliseconds()),
			})
		}
		if len(newBaseUrls) > 0 {
			op.ChannelBaseUrlUpdate(channel.ID, newBaseUrls)
		}
	}
}
