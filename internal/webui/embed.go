package webui

import "embed"

// Static holds the embedded WebUI assets (D8).
//
//go:embed static/*
var Static embed.FS
