package worker

import (
	"context"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
)

func AutoAddGroupItem(id int, ctx context.Context) error {
	group, err := op.GroupGet(id, ctx)
	if err != nil {
		return err
	}
	channels, err := op.ChannelList(ctx)
	if err != nil {
		return err
	}
	keys := make([]model.GroupIDAndLLMName, 0)
	for _, channel := range channels {
		modelNames := strings.Split(channel.Model+","+channel.CustomModel, ",")
		for _, modelName := range modelNames {
			if modelName == "" {
				continue
			}
			if strings.Contains(strings.ToLower(modelName), strings.ToLower(group.Name)) {
				keys = append(keys, model.GroupIDAndLLMName{ChannelID: channel.ID, ModelName: modelName})
			}
		}
	}
	if err := op.GroupItemBatchAdd(id, keys, ctx); err != nil {
		return err
	}
	return nil
}
