package store

import "strings"

// Placeholders 生成 n 个问号占位符（逗号分隔），供 IN (...) 查询拼接；
// 归档、瘦身与附件模块共用同一实现。
func Placeholders(n int) string {
	if n == 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
