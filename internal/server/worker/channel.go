package worker

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/price"
	"github.com/alvindingpeng/switchyard/internal/utils/log"
)

func AutoGroup(channelID int, channelName, channelModel, customModel string, autoGroupType model.AutoGroupType) {
	if autoGroupType == model.AutoGroupTypeNone {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	groups, err := op.GroupList(ctx)
	if err != nil {
		log.Errorf("get group list failed: %v", err)
		return
	}

	modelNames := strings.Split(channelModel+","+customModel, ",")
	for _, modelName := range modelNames {
		if modelName == "" {
			continue
		}
		for _, group := range groups {
			var matched bool
			switch autoGroupType {
			case model.AutoGroupTypeExact:
				// 准确匹配：模型名称与分组名称完全一致
				matched = strings.EqualFold(modelName, group.Name)
			case model.AutoGroupTypeFuzzy:
				// 模糊匹配：模型名称包含分组名称
				matched = strings.Contains(strings.ToLower(modelName), strings.ToLower(group.Name))
			case model.AutoGroupTypeRegex:
				if group.MatchRegex == "" {
					// 如果匹配正则为空，则使用模糊匹配
					matched = strings.EqualFold(modelName, group.Name)
					continue
				}
				// 正则匹配：模型名称与分组名称匹配
				re, err := regexp.Compile(group.MatchRegex)
				if err != nil {
					log.Warnf("compile regex failed: %v", err)
					continue
				}
				matched = re.MatchString(modelName)
			}

			if matched {
				exists := false
				for _, item := range group.Items {
					if item.ChannelID == channelID && item.ModelName == modelName {
						exists = true
						break
					}
				}
				if exists {
					break
				}
				err := op.GroupItemAdd(&model.GroupItem{
					GroupID:   group.ID,
					ChannelID: channelID,
					ModelName: modelName,
					Priority:  len(group.Items) + 1,
					Weight:    1,
				}, ctx)
				if err != nil {
					log.Errorf("add channel %s model %s to group %s failed: %v", channelName, modelName, group.Name, err)
				} else {
					log.Infof("channel %s: model [%s] added to group [%s]", channelName, modelName, group.Name)
				}
				break
			}
		}
	}
}

func CheckAndAddLLMPrice(channelModel, customModel string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		modelNames := strings.Split(channelModel+","+customModel, ",")
		var newModels []string
		for _, modelName := range modelNames {
			if modelName == "" {
				continue
			}
			modelPrice := price.GetLLMPrice(modelName)
			if modelPrice == nil {
				newModels = append(newModels, modelName)
			}
		}
		if len(newModels) > 0 {
			log.Infof("create models: %v", newModels)
			if err := op.LLMBatchCreate(newModels, ctx); err != nil {
				log.Errorf("failed to batch create models: %v", err)
			}
		}
	}()
}
