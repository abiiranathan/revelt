package main

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/abiiranathan/revelt/revelt"
)

// StaticAssets embeds frontend client-side static bundles for production.
//go:embed {{SOURCE_DIR}}/dist/client/*
var StaticAssets embed.FS

// ComponentsFS embeds the frontend component source files so their @mode annotations
// can be parsed at runtime from memory in fully self-contained production binaries.
//go:embed {{SOURCE_DIR}}/{{COMPONENT_DIR}}/*
var ComponentsFS embed.FS

// RenderServerScript embeds the compiled Node.js SSR sidecar. It is written
// out to a temporary file at startup because Node requires a real path on
// disk to exec; it cannot run directly from embedded bytes in memory.
//go:embed {{SOURCE_DIR}}/dist/render-server.cjs
var RenderServerScript []byte

// ConfigJSON embeds revelt.json so the compiled binary carries its own
// configuration and requires no sibling revelt.json file at runtime.
//go:embed revelt.json
var ConfigJSON []byte

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

// ExtractRenderServer writes the embedded render-server.cjs bytes to a
// temporary file and returns its path. Node.js executes scripts from disk,
// so the embedded bytes cannot be exec'd directly; this gives the renderer
// a real path while keeping the deployed artifact to a single binary.
//
// The extracted file is named with the process ID to avoid collisions when
// multiple instances of the binary run concurrently on the same host. It is
// not automatically removed on shutdown; OS temp-directory cleanup (tmpwatch,
// systemd-tmpfiles, reboot, etc.) reclaims it, and the file is harmless to
// leave behind between runs.
func ExtractRenderServer() (string, error) {
	tmpDir := os.TempDir()
	path := filepath.Join(tmpDir, fmt.Sprintf("revelt-render-server-%d.cjs", os.Getpid()))

	if err := os.WriteFile(path, RenderServerScript, 0644); err != nil {
		return "", fmt.Errorf("extracting render-server.cjs to %s: %w", path, err)
	}
	return path, nil
}

// LoadEmbeddedConfig parses the embedded revelt.json and scans `@mode` component
// annotations directly from the embedded source filesystem, ensuring the running
// application requires no physical files on disk in production.
func LoadEmbeddedConfig() (*revelt.ProjectConfig, error) {
	cfg, err := revelt.LoadConfigFromFS(ComponentsFS, ConfigJSON)
	if err != nil {
		return nil, fmt.Errorf("parsing embedded configuration: %w", err)
	}
	return cfg, nil
}
