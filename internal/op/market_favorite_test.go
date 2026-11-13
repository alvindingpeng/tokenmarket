package op

import (
	"path/filepath"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// marketFavoriteDB 临时库。收藏走 db.GetDB(), 必须走 InitDB。
func marketFavoriteDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "fav.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
}

// TestMarketFavoriteAddIsIdempotent 验证重复收藏不会产生第二行。
//
// 幂等不是"讲究": 收藏按钮在弱网下会被连点, 两个标签页也可能同时点。
// 若不幂等, 用户会看到收藏数莫名变大, 而取消收藏只能删掉其中一行 —— 于是"取消一次还在收藏里"。
func TestMarketFavoriteAddIsIdempotent(t *testing.T) {
	marketFavoriteDB(t)
	for i := 0; i < 3; i++ {
		if err := MarketFavoriteAdd(1, "claude-sonnet-5"); err != nil {
			t.Fatalf("add #%d: %v", i, err)
		}
	}
	names, err := MarketFavoriteList(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "claude-sonnet-5" {
		t.Fatalf("want exactly one favorite, got %v", names)
	}
}

// TestMarketFavoriteRemoveIsIdempotent 验证取消一个不存在的收藏不报错。
func TestMarketFavoriteRemoveIsIdempotent(t *testing.T) {
	marketFavoriteDB(t)
	if err := MarketFavoriteRemove(1, "never-favorited"); err != nil {
		t.Fatalf("removing absent favorite must not error: %v", err)
	}
}

// TestMarketFavoriteTrimsWhitespace 验证首尾空白被去掉。
//
// 这条是真实可用性问题: 带空格的收藏在广场按名匹配时永远匹配不到,
// 表现为"收藏了但筛选后看不到", 而库里看起来一切正常 —— 极难排查。
func TestMarketFavoriteTrimsWhitespace(t *testing.T) {
	marketFavoriteDB(t)
	if err := MarketFavoriteAdd(2, "  glm-5.3-flash  "); err != nil {
		t.Fatal(err)
	}
	names, _ := MarketFavoriteList(2)
	if len(names) != 1 || names[0] != "glm-5.3-flash" {
		t.Fatalf("whitespace must be trimmed, got %q", names)
	}
}

// TestMarketFavoriteRejectsEmpty 验证空名被拒。
func TestMarketFavoriteRejectsEmpty(t *testing.T) {
	marketFavoriteDB(t)
	if err := MarketFavoriteAdd(3, "   "); err == nil {
		t.Fatal("blank model name must be rejected")
	}
}

// TestMarketFavoriteIsPerUser 验证收藏按用户隔离。
//
// 这条守的是越权读取: 若查询漏了 user_id 条件, 用户 A 会看到 B 的收藏, 而接口看起来完全正常。
func TestMarketFavoriteIsPerUser(t *testing.T) {
	marketFavoriteDB(t)
	if err := MarketFavoriteAdd(10, "a-model"); err != nil {
		t.Fatal(err)
	}
	if err := MarketFavoriteAdd(11, "b-model"); err != nil {
		t.Fatal(err)
	}
	a, _ := MarketFavoriteList(10)
	if len(a) != 1 || a[0] != "a-model" {
		t.Fatalf("user 10 must only see own favorite, got %v", a)
	}
	b, _ := MarketFavoriteList(11)
	if len(b) != 1 || b[0] != "b-model" {
		t.Fatalf("user 11 must only see own favorite, got %v", b)
	}
}

var _ = model.MarketFavorite{}
