package webui

import "embed"

// Files are packaged with the manager; no external CDN is required.
//go:embed index.html reviewed-ui.js setup-wizard.js playback-modes.js app.js live.css
var Files embed.FS
