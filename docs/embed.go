// Package docs 以 embed 暴露仓库里维护的公开文档。
package docs

import _ "embed"

//go:embed api.md
var APIMarkdown string
