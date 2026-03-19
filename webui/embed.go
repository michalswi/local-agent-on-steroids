package webui

import (
	"embed"
)

//go:embed webstatic/favicon.png
var StaticFiles embed.FS

//go:embed prompts/*.md
var PromptFiles embed.FS
