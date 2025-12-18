package worker

import (
	"context"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/price"
	"github.com/alvindingpeng/switchyard/internal/utils/log"
)

func AutoGroup(channelID int, channelName, channelModel, customModel string) {
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
			if strings.Contains(strings.ToLower(modelName), strings.ToLower(group.Name)) {
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
		for _, modelName := range modelNames {
			if modelName == "" {
				continue
			}
			modelPrice := price.GetLLMPrice(modelName)
			if modelPrice == nil {
				log.Infof("model %s price not found,create", modelName)
				err := op.LLMCreate(model.LLMInfo{Name: modelName}, ctx)
				if err != nil {
					log.Errorf("create model: %s", modelName)
				}
			}
		}
	}()
}
