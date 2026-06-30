package main

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
)

// StaticAssets embeds frontend client-side static bundles for production.
//go:embed {{SOURCE_DIR}}/dist/client/*
var StaticAssets embed.FS

// GetStaticFS returns the static directory filesystem.
// If running in development (detected via local file assets), it falls
// back to direct OS filesystem serving for real-time asset updates.
func GetStaticFS() (http.FileSystem, error) {
	if _, err := os.Stat("{{SOURCE_DIR}}/dist/client"); err == nil {
		return http.Dir("{{SOURCE_DIR}}/dist/client"), nil
	}

	subFS, err := fs.Sub(StaticAssets, "{{SOURCE_DIR}}/dist/client")
	if err != nil {
		return nil, err
	}
	return http.FS(subFS), nil
}
