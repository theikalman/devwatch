// Package web holds the dashboard's static files, embedded into the binary.
package web

import "embed"

//go:embed index.html favicon.svg *.js *.css vendor
var FS embed.FS
