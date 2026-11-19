package handlers

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/gin-gonic/gin"
)

func TestUpdateUserBalanceDoesNotSkipCachedEqualTarget(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "admin-balance.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	user, err := op.UserCreate("admin-balance-target", "", "password123", model.RoleUser, model.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.UserDelete(user.ID) })
	// 模拟数据库已有充值, 缓存仍为创建时余额 0。目标 0 也必须进入调账事务。
	if err := db.GetDB().Model(&model.User{}).Where("id = ?", user.ID).Update("balance", 10).Error; err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/user/manage/update", bytes.NewBufferString(fmt.Sprintf(`{"id":%d,"balance":0}`, user.ID)))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user_id", int(user.ID))
	c.Set("role", string(model.RoleAdmin))
	updateUser(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var fresh model.User
	if err := db.GetDB().First(&fresh, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if fresh.Balance != 0 {
		t.Fatalf("cached equality skipped adjustment: balance=%v", fresh.Balance)
	}
	var entries []model.BillingLedger
	if err := db.GetDB().Where("user_id = ? AND kind = ?", user.ID, model.LedgerKindAdjust).Find(&entries).Error; err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Amount != -10 {
		t.Fatalf("adjustment ledger=%+v", entries)
	}
}
