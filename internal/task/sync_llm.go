package task

import (
	"context"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/client"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/worker"
	"github.com/alvindingpeng/switchyard/internal/utils/log"
)

var lastSyncTime = time.Now()

func SyncLLMTask() {
	log.Debugf("sync LLM task started")
	startTime := time.Now()
	defer func() {
		log.Debugf("sync LLM task finished, sync time: %s", time.Since(startTime))
	}()
	ctx := context.Background()
	channels, err := op.ChannelList(ctx)
	if err != nil {
		log.Errorf("failed to list channels: %v", err)
		return
	}

	for _, channel := range channels {
		if !channel.AutoSync {
			continue
		}
		models, err := client.FetchLLMName(ctx, channel)
		if err != nil {
			log.Warnf("failed to fetch models for channel %s: %v", channel.Name, err)
			continue
		}

		newModelStr := strings.Join(models, ",")
		if channel.Model == newModelStr {
			continue
		}

		// 解析新旧模型集合
		oldModels := parseModels(channel.Model)
		newModels := parseModels(newModelStr)

		// 计算消失的模型和新增的模型
		disappearedModels, addedModels := diffModels(oldModels, newModels)

		// 更新渠道模型
		channel.Model = newModelStr
		if err := op.ChannelUpdate(&channel, ctx); err != nil {
			log.Errorf("failed to update channel %s: %v", channel.Name, err)
			continue
		}
		// 批量删除消失的模型对应的 GroupItem
		if len(disappearedModels) > 0 {
			log.Infof("deleted channel %s models: %v", channel.Name, disappearedModels)
			keys := make([]model.GroupIDAndLLMName, len(disappearedModels))
			for i, m := range disappearedModels {
				keys[i] = model.GroupIDAndLLMName{ChannelID: channel.ID, ModelName: m}
			}
			if err := op.GroupItemBatchDelByChannelAndModels(keys, ctx); err != nil {
				log.Errorf("failed to batch delete group items for channel %s: %v", channel.Name, err)
			}
		}

		// 自动分组新增的模型
		if len(addedModels) > 0 {
			log.Infof("added channel %s models: %v", channel.Name, addedModels)
			worker.AutoGroup(channel.ID, channel.Name, strings.Join(addedModels, ","), "", channel.AutoGroup)
		}
	}
	lastSyncTime = time.Now()
}

func GetLastSyncTime() time.Time {
	return lastSyncTime
}

// parseModels 解析逗号分隔的模型字符串为集合
func parseModels(modelStr string) map[string]struct{} {
	result := make(map[string]struct{})
	if modelStr == "" {
		return result
	}
	for _, m := range strings.Split(modelStr, ",") {
		m = strings.TrimSpace(m)
		if m != "" {
			result[m] = struct{}{}
		}
	}
	return result
}

// diffModels 比较新旧模型集合,返回消失的模型和新增的模型
func diffModels(oldModels, newModels map[string]struct{}) (disappeared, added []string) {
	// 找出消失的模型(在旧集合中但不在新集合中)
	for m := range oldModels {
		if _, exists := newModels[m]; !exists {
			disappeared = append(disappeared, m)
		}
	}
	// 找出新增的模型(在新集合中但不在旧集合中)
	for m := range newModels {
		if _, exists := oldModels[m]; !exists {
			added = append(added, m)
		}
	}
	return
}
