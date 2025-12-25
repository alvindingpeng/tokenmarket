package task

import (
	"context"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/utils/log"
)

func CleanLLMTask() {
	log.Debugf("clean LLM task started")
	startTime := time.Now()
	defer func() {
		log.Debugf("clean LLM task finished, clean time: %s", time.Since(startTime))
	}()
	ctx := context.Background()
	channels, err := op.ChannelList(ctx)
	if err != nil {
		log.Errorf("failed to list channels: %v", err)
		return
	}
	channelModels := make(map[string]struct{})
	for _, channel := range channels {
		models := strings.Split(channel.Model+","+channel.CustomModel, ",")
		for _, model := range models {
			model = strings.TrimSpace(model)
			if model == "" {
				continue
			}
			channelModels[model] = struct{}{}
		}
	}
	llmModels, err := op.LLMList(ctx)
	if err != nil {
		log.Errorf("failed to list LLM models: %v", err)
		return
	}
	var deletedModels []string
	for _, model := range llmModels {
		if _, ok := channelModels[model.Name]; !ok {
			deletedModels = append(deletedModels, model.Name)
		}
	}
	if len(deletedModels) > 0 {
		log.Infof("deleted LLM models: %v", deletedModels)
		if err := op.LLMBatchDelete(deletedModels, ctx); err != nil {
			log.Errorf("failed to batch delete LLM models: %v", err)
		}
	}
}
