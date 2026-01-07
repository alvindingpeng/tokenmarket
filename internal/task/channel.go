package task

import (
	"context"
	"time"

	"github.com/alvindingpeng/switchyard/internal/helper"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/utils/log"
)

func ChannelBaseUrlDelayTask() {
	log.Debugf("channel base url delay task started")
	startTime := time.Now()
	defer func() {
		log.Debugf("channel base url delay task finished, update time: %s", time.Since(startTime))
	}()
	channels, err := op.ChannelList(context.Background())
	if err != nil {
		log.Errorf("failed to list channels: %v", err)
		return
	}
	for _, channel := range channels {
		helper.ChannelBaseUrlDelayUpdate(channel)
	}
}
