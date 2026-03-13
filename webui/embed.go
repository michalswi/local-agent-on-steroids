package webui

import (
	"embed"
)

//go:embed webstatic/favicon.png
var StaticFiles embed.FS
