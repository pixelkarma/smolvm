package main

import (
	"embed"
	"io/fs"
)

//go:embed static/xterm/*
var staticFS embed.FS

func embeddedStaticFS() (fs.FS, error) {
	return fs.Sub(staticFS, "static")
}
