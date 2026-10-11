package alert

import (
	"strings"
	"testing"
)

// TestEscapeTelegramMarkdown 校验实体起始字符被转义。
// 标题与正文是任意文本, 告警错误里下划线/星号/反引号很常见, 不转义 Telegram 会回
// 400 "Can't parse entities", 整条告警发不出去。
func TestEscapeTelegramMarkdown(t *testing.T) {
	got := escapeTelegramMarkdown("a_b*c`d[e\\f")
	want := "a\\_b\\*c\\`d\\[e\\\\f"
	if got != want {
		t.Fatalf("转义结果不符\n got: %q\nwant: %q", got, want)
	}
}

// TestEscapeTelegramMarkdownKeepsPlain 校验普通文本不被过度改写, 消息内容保持原样。
func TestEscapeTelegramMarkdownKeepsPlain(t *testing.T) {
	const plain = "2026-10-12 01:30:00 hello 世界"
	if got := escapeTelegramMarkdown(plain); got != plain {
		t.Fatalf("普通文本被改写: %q", got)
	}
}

// TestTelegramErrorSurfacesDescription 校验 Telegram 的 description 被带出来。
// 只报 HTTP 状态码等于把唯一的排查线索丢掉(实测用户只看到 "api 400", 无从判断是 chat_id 还是 token 问题)。
func TestTelegramErrorSurfacesDescription(t *testing.T) {
	err := telegramError(400, []byte("{\"ok\":false,\"error_code\":400,\"description\":\"Bad Request: chat not found\"}"))
	if err == nil || !strings.Contains(err.Error(), "Bad Request: chat not found") {
		t.Fatalf("错误未带出 Telegram description: %v", err)
	}
	if !strings.Contains(err.Error(), "chat_id") {
		t.Fatalf("chat not found 应给出排查提示: %v", err)
	}
}

// TestTelegramErrorFallbackWithoutDescription 校验非 JSON 响应时仍报出状态码。
func TestTelegramErrorFallbackWithoutDescription(t *testing.T) {
	err := telegramError(502, []byte("<html>bad gateway</html>"))
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("无 description 时应回退到状态码: %v", err)
	}
}
