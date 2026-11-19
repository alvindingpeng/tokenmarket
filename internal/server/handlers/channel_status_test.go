package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/op"
)

// TestChannelWriteErrorStatus 守护渠道增删改的状态码映射。
//
// 线上事故(2026-11-03): 出口代理地址指向内网, 被 SSRF 防护拒绝。ChannelUpdate 返回的是
// "输入不合法"这类错误, 但 handler 一律回 500, 前端更新分支又没有 onError —— 于是管理员
// 看到的只是"配置好之后无法保存", 完全无从判断是地址填错还是服务端故障。
// 这张映射一旦退回"一律 500", 同样的排查困境会立刻复现。
func TestChannelWriteErrorStatus(t *testing.T) {
	// 输入不合法 -> 400, 让前端按状态码给出可读提示。
	invalid := &op.InvalidInputError{Reason: errors.New("egress egress-1: upstream host 192.168.1.1 resolves to a private address")}
	if got := channelWriteErrorStatus(invalid); got != http.StatusBadRequest {
		t.Errorf("输入不合法应回 %d, 实得 %d", http.StatusBadRequest, got)
	}

	// 被包装一层后仍要能识别: InvalidInputError.Is 让 errors.Is 穿透包装链。
	// 否则上游给错误加一句上下文, 状态码就悄悄退回 500。
	wrapped := fmt.Errorf("failed to save channel: %w", invalid)
	if got := channelWriteErrorStatus(wrapped); got != http.StatusBadRequest {
		t.Errorf("包装后仍应回 %d, 实得 %d", http.StatusBadRequest, got)
	}

	// 真正的服务端故障必须留在 500: 误降级成 400 会把"库锁住了"说成"你填错了",
	// 反而把排查方向带向用户输入。
	for _, err := range []error{errors.New("database is locked"), errors.New("failed to refresh groups")} {
		if got := channelWriteErrorStatus(err); got != http.StatusInternalServerError {
			t.Errorf("服务端故障应回 %d, 实得 %d", http.StatusInternalServerError, got)
		}
	}
}
