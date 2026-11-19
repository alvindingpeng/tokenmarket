package op

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/alert"
	"github.com/charmbracelet/log"
)

// 外部看门狗日志的读取与告警。
//
// 为什么需要它: switchyard 自己的 /healthz 只回答「这个进程还在不在」。2026-11-02 的实际
// 故障形态是进程以约 1.3 秒一次的速度反复重启(数据库迁移报错), 每一次启动都能短暂地通过
// 存活探针 —— 站内探针与 systemd 的 Restart=always 都无法把「两小时内重启 1192 次」这件事
// 变成管理员看得见的信息。
//
// 因此探活被移到进程之外: deploy/switchyard-watchdog.sh 由 systemd timer 每 60 秒跑一次,
// 连续 3 次探测失败就重启服务, 并把每次探测追加成一行日志。本文件在服务重新起来之后
// 读那份日志, 把停机窗口里发生过什么讲给管理员。
//
// 日志行格式是与部署侧的契约, 见 deploy/README.md:
//
//	<RFC3339 本地时间> <fail|ok|restart> <明细>
//
// 只按空格切成三段解析(SplitN 3), 明细里的空格不影响解析。
const (
	// watchdogLogEnv 覆盖日志路径, 供演练与测试使用。
	watchdogLogEnv = "SWITCHYARD_WATCHDOG_LOG"
	// watchdogDefaultLogPath 与 deploy/switchyard-watchdog.sh 的默认值一致。
	watchdogDefaultLogPath = "/opt/switchyard/data/watchdog-failures.log"

	// watchdogMaxScanBytes 单次最多回看的日志字节数。
	// 看门狗每 60 秒最多写一行, 一行约 80 字节, 一天约 115 KB; 上限 4 MiB 足够,
	// 又能挡住「日志被灌爆」时把内存吃光。
	watchdogMaxScanBytes = 4 << 20

	// watchdogFailAlertAfter 新读到多少次 fail 才告警。
	// 与 internal/task 的 taskAlertAfter 取同一口径(3): 看门狗本身在连续 3 次失败时就会
	// 尝试重启, 运维应当在这条重启记录出现时就被通知。
	watchdogFailAlertAfter = 3

	// watchdogAlertReason 告警去重键。
	// 故意做成常量而不是把失败次数拼进去: 去重靠 (kind, reason) 相等,
	// 次数一变就变成新告警, 一次故障会刷满告警表。次数放在事实字段里。
	watchdogAlertReason = "external watchdog reported failures"
)

// WatchdogLogState 一次巡检读到的看门狗活动。
type WatchdogLogState struct {
	LogPath string
	// NewLines 本次新读到的行数; 0 表示自上次巡检以来看门狗没有写过任何东西。
	NewLines int
	// Failures 新读到的 fail 行数。
	Failures int
	// Restarts 新读到的 restart 行数(看门狗真的重启过服务的次数)。
	Restarts int
	// FirstFailure / LastFailure 新读到的第一条与最后一条 fail 的时间; 零值表示没有 fail。
	FirstFailure time.Time
	LastFailure  time.Time
	// LastRestart 新读到的最后一条 restart 的时间; 零值表示没有 restart。
	LastRestart time.Time
	// LastDetail 最后一条 fail 的明细, 用于告警正文。
	LastDetail string
	// CursorByte 本次读完之后写入游标的字节偏移。
	CursorByte int64
	// Truncated 表示日志超过上限被截断, 只看了尾部; 中间的失败可能没被计入。
	Truncated bool
}

// WatchdogLogPath 返回看门狗日志路径: 环境变量优先, 否则用部署默认值。
func WatchdogLogPath() string {
	if p := strings.TrimSpace(os.Getenv(watchdogLogEnv)); p != "" {
		return p
	}
	return watchdogDefaultLogPath
}

// watchdogCursorPath 游标文件放在日志旁边: <log>.cursor。
//
// 用文件而不是设置表的原因: 这是纯粹的巡检进度, 不该出现在管理员的设置列表里;
// 也不该随备份带到别的机器 —— 备份恢复后从零重扫一遍日志是无害的(顶多多告一次警),
// 而带着别人的游标过来会静默漏掉本机的故障。
func watchdogCursorPath(logPath string) string { return logPath + ".cursor" }

func readWatchdogCursor(path string) int64 {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	offset, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil || offset < 0 {
		return 0
	}
	return offset
}

// WatchdogLogScan 从上次游标处读到文件末尾, 汇总这一次新出现的失败与重启。
//
// 只读新增部分而不是全量重读: 看门狗日志只追加, 全量重读会让每一次重启都重复告警历史上
// 所有的失败。游标按文件长度推进, 并且只在成功读完一个完整快照之后才写回,
// 中途出错时下次会重新读这一段(宁可重复告警, 不可漏告警)。
func WatchdogLogScan() (WatchdogLogState, error) {
	logPath := WatchdogLogPath()
	state := WatchdogLogState{LogPath: logPath}

	file, err := os.Open(logPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// 没装看门狗就没有日志。这不是错误: 是否安装外部看门狗由运维决定,
			// 服务不该因为一个可选组件缺席而报故障。
			return state, nil
		}
		return state, err
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return state, err
	}
	size := info.Size()
	cursorPath := watchdogCursorPath(logPath)
	offset := readWatchdogCursor(cursorPath)
	if offset > size {
		// 日志被轮转或截断。不回到 0 的话会永远读不到任何内容。
		log.Infof("watchdog log shrank (%d -> %d bytes), rescanning from start", offset, size)
		offset = 0
	}
	start := offset
	if size-start > watchdogMaxScanBytes {
		start = size - watchdogMaxScanBytes
		state.Truncated = true
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return state, err
	}
	raw, err := io.ReadAll(io.LimitReader(file, watchdogMaxScanBytes))
	if err != nil {
		return state, err
	}
	text := string(raw)
	if start > offset {
		// 跳到尾部时第一条几乎肯定是被切断的半行, 丢掉它, 否则明细会解析成乱码。
		if idx := strings.IndexByte(text, '\n'); idx >= 0 {
			text = text[idx+1:]
		} else {
			text = ""
		}
	}

	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 3)
		if len(parts) < 2 {
			continue
		}
		stamp, parseErr := time.Parse(time.RFC3339, parts[0])
		if parseErr != nil {
			// 时间戳不认识时仍然按状态计数: 少一个时间点比丢掉一次故障记录好。
			stamp = time.Time{}
		}
		detail := ""
		if len(parts) == 3 {
			detail = parts[2]
		}
		state.NewLines++
		switch parts[1] {
		case "fail":
			state.Failures++
			if state.FirstFailure.IsZero() && !stamp.IsZero() {
				state.FirstFailure = stamp
			}
			if !stamp.IsZero() {
				state.LastFailure = stamp
			}
			state.LastDetail = detail
		case "restart":
			state.Restarts++
			if !stamp.IsZero() {
				state.LastRestart = stamp
			}
		case "ok":
			// ok 行只用于让「服务当时是健康的」这件事留在日志里, 不需要单独统计。
		}
	}

	state.CursorByte = size
	if err := os.WriteFile(cursorPath, []byte(strconv.FormatInt(size, 10)), 0o600); err != nil {
		// 游标写不进去只会导致下一次重复告警, 不影响本次结论, 所以不把它当失败返回。
		log.Warnf("watchdog cursor write failed (%s): %v", cursorPath, err)
	}
	return state, nil
}

// WatchdogAlertRun 巡检外部看门狗日志, 有新故障时告警。
//
// 调用点有两处, 缺一不可:
//   - 服务启动之后(cmd/start.go): 看门狗重启过服务这件事, 只有等服务再次起来才有人能记录它;
//   - 周期任务: 覆盖「看门狗探测连续失败但还没重启」的情况(例如 10 分钟重启闸门还没到期)。
//
// 返回 error 只在日志读不动时(权限、磁盘); 没有新故障不是错误。
func WatchdogAlertRun(ctx context.Context) error {
	state, err := WatchdogLogScan()
	if err != nil {
		return fmt.Errorf("scan watchdog log: %w", err)
	}
	if state.NewLines == 0 {
		return nil
	}
	if state.Failures < watchdogFailAlertAfter && state.Restarts == 0 {
		// 零星失败(1-2 次)是网络抖动, 看门狗自己会在第 3 次动手; 不打扰管理员。
		log.Infof("watchdog log: %d new line(s), %d failure(s), %d restart(s), no alert",
			state.NewLines, state.Failures, state.Restarts)
		return nil
	}

	log.Warnf("external watchdog reported trouble: failures=%d restarts=%d log=%s",
		state.Failures, state.Restarts, state.LogPath)

	fields := []alert.Field{
		{Label: "看门狗日志", Value: state.LogPath},
		{Label: "新失败次数", Value: fmt.Sprintf("%d 次", state.Failures)},
		{Label: "看门狗重启次数", Value: fmt.Sprintf("%d 次", state.Restarts)},
	}
	if !state.FirstFailure.IsZero() {
		fields = append(fields, alert.Field{Label: "首次失败", Value: state.FirstFailure.Format("2006-01-02 15:04:05 -07:00")})
	}
	if !state.LastFailure.IsZero() {
		fields = append(fields, alert.Field{Label: "最近失败", Value: state.LastFailure.Format("2006-01-02 15:04:05 -07:00")})
	}
	if !state.LastRestart.IsZero() {
		fields = append(fields, alert.Field{Label: "最近重启", Value: state.LastRestart.Format("2006-01-02 15:04:05 -07:00")})
	}
	if state.LastDetail != "" {
		fields = append(fields, alert.Field{Label: "最近明细", Value: state.LastDetail})
	}
	if state.Truncated {
		fields = append(fields, alert.Field{Label: "注意", Value: "日志过长只回看了尾部, 中间的失败未计入"})
	}
	fields = append(fields,
		alert.Field{Label: "说明", Value: "外部看门狗在 /healthz 连续失败后重启了服务: 站内探针无法把这件事变成告警, 只有它在进程之外看得见"},
		alert.Field{Label: "影响", Value: "停机窗口内的请求全部失败, 且原因可能已经在重启后消失"},
		alert.Field{Label: "建议", Value: "按日志时间点对照 journalctl 与 systemd 的 NRestarts 定位重启原因; 若为迁移或配置错误, unit 的 StartLimit 会让它停在 failed 而不是无限重启"},
	)
	RaiseAlertFacts(ctx, AlertFacts{
		Kind:   "watchdog_alert",
		Reason: watchdogAlertReason,
		Fields: fields,
	})
	return nil
}
