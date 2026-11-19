package handlers

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/gin-gonic/gin"
)

func modelPriceTestDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "model_price.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := op.InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}
}

// TestCreateLLMRejectsNegativePrice 负价必须 400 且不落库。
//
// 此前的行为: 只校验名称非空, 负价直接成功落库 —— 而 LLMPrice.Cost 是纯乘法,
// 负价会让"调用反而加钱"。也不能用 500: 这是输入问题, 用 500 会淹没真实故障信号
// (与渠道保存那次同一教训)。
func TestCreateLLMRejectsNegativePrice(t *testing.T) {
	modelPriceTestDB(t)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/create", createLLM)

	body := `{"name":"gpt-negative","input":-1,"output":0.5}`
	req := httptest.NewRequest(http.MethodPost, "/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("负价未被拒: got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "must not be negative") {
		t.Fatalf("响应未说明原因: %s", w.Body.String())
	}

	var count int64
	if err := db.GetDB().Model(&model.LLMInfo{}).Where("name = ?", "gpt-negative").Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("被拒的负价仍落库了 %d 行", count)
	}
}

// TestUpdateLLMRejectsNegativePrice 更新同样要拦 —— 否则先建合法的再改成负的就能绕过。
func TestUpdateLLMRejectsNegativePrice(t *testing.T) {
	modelPriceTestDB(t)
	if err := op.LLMCreate(model.LLMInfo{Name: "gpt-upd", LLMPrice: model.LLMPrice{Input: 1, Output: 2}}, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/update", updateLLM)

	body := `{"name":"gpt-upd","input":1,"output":-5}`
	req := httptest.NewRequest(http.MethodPost, "/update", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("负价更新未被拒: got %d: %s", w.Code, w.Body.String())
	}

	var fresh model.LLMInfo
	if err := db.GetDB().Where("name = ?", "gpt-upd").First(&fresh).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if fresh.Output != 2 {
		t.Fatalf("被拒的更新改动了已有价格: output=%v, 期望仍为 2", fresh.Output)
	}
}

// TestCreateLLMAcceptsFreeModel 全 0 的免费模型是合法配置, 不能一并拦掉。
func TestCreateLLMAcceptsFreeModel(t *testing.T) {
	modelPriceTestDB(t)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/create", createLLM)

	body := `{"name":"gpt-free","input":0,"output":0}`
	req := httptest.NewRequest(http.MethodPost, "/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("免费模型被拒: got %d: %s", w.Code, w.Body.String())
	}
}
