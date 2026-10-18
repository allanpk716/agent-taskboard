// Package docs exposes repository-maintained public documentation for embedding.
package docs

import _ "embed"

//go:embed api.md
var APIMarkdown string
