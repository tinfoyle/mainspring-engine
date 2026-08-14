package assets

import "embed"

// Files contains the browser assets served by the Go applications.
//
//go:embed app.css htmx.min.js htmx-sse.min.js boardroom.js messenger.js baseline.js your_turn.js v2.css v2.js
var Files embed.FS
