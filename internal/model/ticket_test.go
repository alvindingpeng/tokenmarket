package model

import "testing"

// TestTicketCanTransition 状态机矩阵: 表内流转合法, 表外(含自环)一律拒绝。
func TestTicketCanTransition(t *testing.T) {
	allowed := [][2]string{
		{TicketStatusOpen, TicketStatusInProgress},
		{TicketStatusOpen, TicketStatusClosed},
		{TicketStatusInProgress, TicketStatusOpen},
		{TicketStatusInProgress, TicketStatusResolved},
		{TicketStatusInProgress, TicketStatusClosed},
		{TicketStatusResolved, TicketStatusInProgress},
		{TicketStatusResolved, TicketStatusClosed},
		{TicketStatusClosed, TicketStatusOpen}, // 用户可重新打开
	}
	for _, pair := range allowed {
		if !TicketCanTransition(pair[0], pair[1]) {
			t.Fatalf("%s -> %s should be legal", pair[0], pair[1])
		}
	}

	rejected := [][2]string{
		{TicketStatusOpen, TicketStatusOpen}, // 自环不是流转
		{TicketStatusInProgress, TicketStatusInProgress},
		{TicketStatusResolved, TicketStatusResolved},
		{TicketStatusClosed, TicketStatusClosed},
		{TicketStatusOpen, TicketStatusResolved},     // 必须经过 in_progress
		{TicketStatusResolved, TicketStatusOpen},     // 重开只能从 closed 走
		{TicketStatusClosed, TicketStatusInProgress}, // 已关闭不能直接回处理中
		{TicketStatusClosed, TicketStatusResolved},
		{"", TicketStatusOpen}, // 未知状态
		{TicketStatusOpen, "bogus"},
	}
	for _, pair := range rejected {
		if TicketCanTransition(pair[0], pair[1]) {
			t.Fatalf("%s -> %s should be rejected", pair[0], pair[1])
		}
	}
}

// TestTicketListRequestNormalize 分页缺省与上限。
func TestTicketListRequestNormalize(t *testing.T) {
	var req TicketListRequest
	req.Normalize()
	if req.Page != 1 || req.PageSize != 20 {
		t.Fatalf("defaults: page=%d page_size=%d", req.Page, req.PageSize)
	}
	req = TicketListRequest{Page: 3, PageSize: 50}
	req.Normalize()
	if req.Page != 3 || req.PageSize != 50 {
		t.Fatalf("kept: page=%d page_size=%d", req.Page, req.PageSize)
	}
	req = TicketListRequest{Page: -5, PageSize: 1000}
	req.Normalize()
	if req.Page != 1 || req.PageSize != 100 {
		t.Fatalf("clamped: page=%d page_size=%d", req.Page, req.PageSize)
	}
}
