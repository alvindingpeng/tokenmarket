package op

import (
	"context"
	"fmt"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/utils/cache"
	"github.com/alvindingpeng/switchyard/internal/utils/log"
)

var channelCache = cache.New[int, model.Channel](16)

func ChannelList(ctx context.Context) ([]model.Channel, error) {
	channels := make([]model.Channel, 0, channelCache.Len())
	for _, channel := range channelCache.GetAll() {
		channels = append(channels, channel)
	}
	return channels, nil
}

func ChannelCreate(channel *model.Channel, ctx context.Context) error {
	if err := db.GetDB().WithContext(ctx).Create(channel).Error; err != nil {
		return err
	}
	channelCache.Set(channel.ID, *channel)
	return nil
}

func ChannelUpdate(channel *model.Channel, ctx context.Context) error {
	oldChannel, ok := channelCache.Get(channel.ID)
	if !ok {
		return fmt.Errorf("channel not found")
	}
	if oldChannel == *channel {
		return nil
	}
	if err := db.GetDB().WithContext(ctx).Save(channel).Error; err != nil {
		return err
	}
	channelCache.Set(channel.ID, *channel)
	return nil
}

func ChannelDel(id int, ctx context.Context) error {
	channel, ok := channelCache.Get(id)
	if !ok {
		return fmt.Errorf("channel not found")
	}
	if err := db.GetDB().WithContext(ctx).Delete(channel).Error; err != nil {
		return err
	}
	channelCache.Del(channel.ID)
	return nil
}

func ChannelModelList(ctx context.Context) ([]string, error) {
	models := []string{}
	for _, channel := range channelCache.GetAll() {
		if channel.Enabled {
			spl := strings.Split(channel.Model, ",")
			for _, model := range spl {
				models = append(models, channel.Name+"/"+model)
			}
		}
	}
	return models, nil
}

func channelRefreshCache(ctx context.Context) error {
	channels := []model.Channel{}
	if err := db.GetDB().WithContext(ctx).Find(&channels).Error; err != nil {
		log.Errorf("failed to get channels: %v", err)
		return err
	}
	for _, channel := range channels {
		channelCache.Set(channel.ID, channel)
	}
	return nil
}
