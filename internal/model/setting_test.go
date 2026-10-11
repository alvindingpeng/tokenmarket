package model

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// TestDefaultSettingsCoversEveryKey 守护设置键的播种完整性。
// 只有出现在 DefaultSettings() 里的键才会在启动时落库; 漏播种的键不存在于 settingCache,
// SettingGetString 与 SettingSetString 会一律返回 "setting not found" ——
// 表现为前端能填能点, 保存却必然失败。这里直接解析源码常量, 新增键时无需手工维护清单。
func TestDefaultSettingsCoversEveryKey(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "setting.go", nil, 0)
	if err != nil {
		t.Fatalf("parse setting.go: %v", err)
	}

	seeded := make(map[SettingKey]bool)
	for _, setting := range DefaultSettings() {
		seeded[setting.Key] = true
	}

	declared := 0
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		// 常量块允许省略类型, 沿用上一条的类型。
		var lastType string
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if ident, ok := value.Type.(*ast.Ident); ok {
				lastType = ident.Name
			}
			if lastType != "SettingKey" {
				continue
			}
			for _, expr := range value.Values {
				lit, ok := expr.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				raw, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("unquote %s: %v", lit.Value, err)
				}
				declared++
				key := SettingKey(raw)
				// 内部键(如 auth_secret)由启动流程自行生成, 不进默认值。
				if IsInternalSetting(key) {
					continue
				}
				if !seeded[key] {
					t.Errorf("设置键 %q 未在 DefaultSettings() 中播种: 该键不会落库, 读写都会以 \"setting not found\" 失败", raw)
				}
			}
		}
	}

	if declared == 0 {
		t.Fatal("未从 setting.go 解析到任何 SettingKey 常量, 该守护测试已失效")
	}
}
