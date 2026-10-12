package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

// newEchoEngine 建一个带请求体上限的引擎, /echo 把请求体读到底:
// 用它回传的字节数与 truncated 标记判断限制有没有生效。
// 注意 Gin 的 Use 只作用于其后注册的路由, 故中间件先挂、路由后注册。
func newEchoEngine(defaultMax int64, rules ...BodyLimitRule) *gin.Engine {
	r := gin.New()
	r.Use(BodyLimit(defaultMax, rules...))
	r.POST("/echo", func(c *gin.Context) {
		b, err := io.ReadAll(c.Request.Body)
		c.JSON(http.StatusOK, gin.H{"bytes": len(b), "truncated": err != nil})
	})
	// 其余路径由 NoRoute 兜住, 让测试可以拿真实前缀(/v1/... 与 /api/v1/setting/import)
	// 去命中规则, 而不必为每条路径单独注册一遍同样的处理函数。
	r.NoRoute(func(c *gin.Context) {
		b, err := io.ReadAll(c.Request.Body)
		c.JSON(http.StatusOK, gin.H{"bytes": len(b), "truncated": err != nil})
	})
	return r
}

// post 发一个带 Content-Length 的 POST。
func post(path string, size int) *http.Request {
	return httptest.NewRequest(http.MethodPost, path, bytes.NewReader(bytes.Repeat([]byte("a"), size)))
}

// TestBodyLimitRejectsByContentLength 校验声明了超限 Content-Length 的请求直接吃 413:
// 一个字节都不必读进来。上限写进响应, 让调用方知道该缩到什么程度。
func TestBodyLimitRejectsByContentLength(t *testing.T) {
	r := newEchoEngine(16)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, post("/echo", 64))

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`exceeds 16 bytes`)) {
		t.Fatalf("响应应写明上限, got %s", w.Body.String())
	}
}

// TestBodyLimitPassesSmallBody 校验正常大小的请求原样通过。
func TestBodyLimitPassesSmallBody(t *testing.T) {
	r := newEchoEngine(16 << 20)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, post("/echo", 128))

	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"bytes":128`)) {
		t.Fatalf("小请求应原样通过, got %d %s", w.Code, w.Body.String())
	}
}

// TestBodyLimitTruncatesUndeclaredBody 校验 Content-Length 缺失(分块传输)时由
// MaxBytesReader 兜底: 处理函数读到错误, 而不是无上限地吃进内存。
func TestBodyLimitTruncatesUndeclaredBody(t *testing.T) {
	r := newEchoEngine(16)
	req := httptest.NewRequest(http.MethodPost, "/echo", nil)
	req.Body = io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("a"), 4096)))
	req.ContentLength = -1
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if !bytes.Contains(w.Body.Bytes(), []byte(`"truncated":true`)) {
		t.Fatalf("长度未知时仍应截断, got %d %s", w.Code, w.Body.String())
	}
}

// TestBodyLimitExemptPrefix 校验豁免前缀: 整库导入的体积由库本身决定,
// 生产库上百 MB, 套上管理端上限等于把恢复数据的通道堵死。
func TestBodyLimitExemptPrefix(t *testing.T) {
	r := newEchoEngine(16, BodyLimitRule{Prefix: "/api/v1/setting/import", MaxBytes: 0})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setting/import", bytes.NewReader(bytes.Repeat([]byte("a"), 4096)))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"bytes":4096`)) {
		t.Fatalf("豁免路径应完整收下载荷, got %d %s", w.Code, w.Body.String())
	}
}

// TestBodyLimitRuleTier 校验转发接口走更宽的一档而不是全局上限。
func TestBodyLimitRuleTier(t *testing.T) {
	r := newEchoEngine(16,
		BodyLimitRule{Prefix: "/v1", MaxBytes: 1 << 20},
		BodyLimitRule{Prefix: "/api/v1/setting/import", MaxBytes: 0},
	)

	for _, tc := range []struct {
		path string
		code int
	}{
		{"/echo", http.StatusRequestEntityTooLarge},
		{"/v1/chat/completions", http.StatusOK},
		{"/api/v1/setting/import", http.StatusOK},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, post(tc.path, 4096))
		if w.Code != tc.code {
			t.Fatalf("%s status = %d, want %d (%s)", tc.path, w.Code, tc.code, w.Body.String())
		}
	}
}

// TestBodyLimitSkipsGet 校验无请求体的方法完全跳过检查。
func TestBodyLimitSkipsGet(t *testing.T) {
	r := gin.New()
	r.Use(BodyLimit(1))
	r.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ping", nil))

	if w.Code != http.StatusOK || w.Body.String() != "pong" {
		t.Fatalf("GET 不应受限, got %d %s", w.Code, w.Body.String())
	}
}
