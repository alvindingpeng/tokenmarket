package handlers

import "strings"

// sanitizeCSVCell 防止 CSV 公式注入: 单元格以 =+-@ 开头时(含制表符/回车/空白等前缀变体),
// 前置单引号让 Excel 按文本处理而非当作公式执行。管理员用 Excel 打开导出文件是常见动作,
// 而 api_key_name/model/error/detail 等字段可能含用户可控内容, 不清理会变成数据外带/命令执行通道。
func sanitizeCSVCell(s string) string {
	trimmed := strings.TrimLeft(s, " \t\r\n")
	if trimmed == "" {
		return s
	}
	switch trimmed[0] {
	case '=', '+', '-', '@':
		return "'" + s
	}
	return s
}
