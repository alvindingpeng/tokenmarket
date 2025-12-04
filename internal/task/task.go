package task

import (
	"time"

	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/price"
)

const taskInterval = 10 * time.Minute

func RUN() {
	for {
		price.UpdateLLMPriceTask()
		op.StatsSaveDBTask()
		time.Sleep(taskInterval)
	}
}
