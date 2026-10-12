package op

import (
	"errors"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// videoJobTTL 是任务归属的保留时长。上游的任务本身也会过期, 超过这个时间的记录不再有查询价值,
// 留着只会让表无限增长。取 24 小时是因为视频任务极少跨越这个时间还在被轮询。
const videoJobTTL = 24 * time.Hour

// VideoJobSave 落库一次生视频任务的归属。同一任务 ID 重复写入时覆盖, 重试或上游换发 ID 都不会留下孤儿行。
func VideoJobSave(job model.VideoJob) error {
	if job.JobID == "" {
		return errors.New("video job id is required")
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now()
	}
	return db.GetDB().Save(&job).Error
}

// VideoJobGet 按任务 ID 取回归属并校验发起人。任务不存在与不属于该用户返回同一个错误,
// 由调用方一律回 404: 区分这两种情况会让任务 ID 变成探测他人任务的工具。
func VideoJobGet(jobID string, userID uint) (model.VideoJob, error) {
	var job model.VideoJob
	if err := db.GetDB().Where("job_id = ? AND user_id = ?", jobID, userID).First(&job).Error; err != nil {
		return model.VideoJob{}, errors.New("video job not found")
	}
	return job, nil
}

// VideoJobDelete 删除任务归属记录; 上游任务已经不存在时也照常删除本地记录。
func VideoJobDelete(jobID string) error {
	return db.GetDB().Where("job_id = ?", jobID).Delete(&model.VideoJob{}).Error
}

// VideoJobCleanup 清理超过保留期的任务归属, 返回删除的行数。
func VideoJobCleanup() (int64, error) {
	result := db.GetDB().Where("created_at < ?", time.Now().Add(-videoJobTTL)).Delete(&model.VideoJob{})
	return result.RowsAffected, result.Error
}
