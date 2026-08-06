package assets

import "embed"

// Files contains the browser assets served by the Go applications.
//
//go:embed app.css htmx.min.js htmx-sse.min.js
var Files embed.FS
