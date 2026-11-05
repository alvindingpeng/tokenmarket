package op

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/charmbracelet/log"
)

// 归档目录是唯一会随"运维动作"而不是业务量增长的目录: data/archives 下的 NDJSON 只被写入和列举,
// 从来没有删除逻辑。B3 实测 20 个文件 33 MB, 且部署备份目录合计 1.2 GB。
//
// 这里的清理刻意做得很窄, 因为删错的东西是不可恢复的:
//   - 只认本服务自己生成的两个文件名(relay-YYYYMMDD.ndjson 与 audit/audit-YYYYMMDD.ndjson),
//     运维手工放进去的任何文件、任何子目录都不在删除面内;
//   - 日期取自文件名而不是 mtime: 归档是追加写, mtime 会被"给旧归档补写一条"抬高,
//     按 mtime 判会把该淘汰的文件永久续命, 这与留存口径相反;
//   - 无论多老, 永远保留最近 archiveRetentionKeepMinimum 份, 避免时钟跳变或长期停机后
//     一次把全部历史归档清空;
//   - 只删文件不删目录, 单条失败只记 Warn 不中断, 幂等可重复执行。
const archiveRetentionKeepMinimum = 3

// ArchiveRetentionDays 返回归档文件留存天数, 非法值回退 180 天。
func ArchiveRetentionDays() int {
	days, err := SettingGetInt(model.SettingKeyArchiveRetentionDays)
	if err != nil || days < 1 {
		return 180
	}
	if days > 3650 {
		return 3650
	}
	return days
}

// ArchivePruneResult 一次归档文件清理的结果。
type ArchivePruneResult struct {
	Cutoff time.Time `json:"cutoff"`
	Days   int       `json:"retention_days"`
	Kept   int       `json:"kept"`
	Pruned int       `json:"pruned"`
	FreedB int64     `json:"freed_bytes"`
}

// archiveFileStamp 从受管归档文件名里取日期; 不属于本服务的文件返回 false。
// prefix 形如 "relay-" 或 "audit-" 由调用方给出, 目录区分子目录。
func archiveFileStamp(name, prefix string) (time.Time, bool) {
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".ndjson") {
		return time.Time{}, false
	}
	stamp := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".ndjson")
	parsed, err := time.Parse("20060102", stamp)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

// pruneArchiveDir 清理单个归档目录里的超期文件, 返回保留数、删除数与释放字节数。
func pruneArchiveDir(dir, prefix string, cutoff time.Time) (kept int, pruned int, freed int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, 0
		}
		log.Warnf("archive retention: read %s failed: %v", dir, err)
		return 0, 0, 0
	}
	type archiveFile struct {
		name  string
		stamp time.Time
		size  int64
	}
	managed := make([]archiveFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue // 子目录(例如 archives/audit 之于 archives)由调用方单独处理, 这里绝不下钻也不删。
		}
		stamp, ok := archiveFileStamp(entry.Name(), prefix)
		if !ok {
			continue // 非本服务产物: 手工文件、临时文件都不是清理对象。
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		managed = append(managed, archiveFile{name: entry.Name(), stamp: stamp, size: info.Size()})
	}
	// 新的在前: 兜底保留看的是"最近几份", 不是"最老几份"。
	sort.Slice(managed, func(i, j int) bool { return managed[i].stamp.After(managed[j].stamp) })
	for index, file := range managed {
		if index < archiveRetentionKeepMinimum {
			kept++
			continue
		}
		if !file.stamp.Before(cutoff) {
			kept++
			continue
		}
		full := filepath.Join(dir, file.name)
		if err := os.Remove(full); err != nil {
			log.Warnf("archive retention: remove %s failed, keeping it for the next run: %v", full, err)
			kept++
			continue
		}
		pruned++
		freed += file.size
	}
	return kept, pruned, freed
}

// ArchiveRetentionRun 按留存期清理调用日志与审计归档文件, 幂等可重复执行。
// 归档文件是数据库清理后的离线副本: 它们过期不再需要, 但删早了就再也补不回来,
// 所以留存天数独立成键(archive_retention_days), 不跟 log_retention_days 的 7 天走。
func ArchiveRetentionRun(ctx context.Context) (ArchivePruneResult, error) {
	days := ArchiveRetentionDays()
	result := ArchivePruneResult{Cutoff: time.Now().AddDate(0, 0, -days).Truncate(24 * time.Hour), Days: days}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	kept, pruned, freed := pruneArchiveDir(ArchiveDir(), "relay-", result.Cutoff)
	result.Kept, result.Pruned, result.FreedB = kept, pruned, freed
	kept, pruned, freed = pruneArchiveDir(AuditArchiveDir(), "audit-", result.Cutoff)
	result.Kept += kept
	result.Pruned += pruned
	result.FreedB += freed
	if result.Pruned > 0 {
		log.Infof("archive retention: cutoff=%s pruned=%d freed=%dB", result.Cutoff.Format("2006-01-02"), result.Pruned, result.FreedB)
	}
	return result, nil
}
