// Package revelt provides infrastructure for server-side rendering Svelte
// and React components via supervised Node.js processes. This file handles
// loading and validating global framework configurations.
package revelt

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Supported component execution modes.
const (
	// ModeSSR indicates a component is rendered only on the server.
	// No client-side bundle or hydration code is shipped to the browser.
	ModeSSR = "ssr"

	// ModeHydrate indicates a component is server-rendered to HTML,
	// and then dynamically hydrated on the client for full interactivity.
	ModeHydrate = "hydrate"

	// ModeClient indicates a component is rendered only on the client.
	// Node SSR is bypassed entirely; the server sends an empty mount container.
	ModeClient = "client"

	// ModeLazyClient indicates a component is rendered only on the client,
	// and its JavaScript chunk is fetched lazily on first use rather than
	// eagerly at page load. Node SSR is bypassed in the same way as ModeClient.
	// Use this for heavy components that are only reachable via navigation
	// (e.g. a chart on a secondary route) to avoid paying their download cost
	// on every page load.
	ModeLazyClient = "lazy-client"
)

// componentExtensions lists the file extensions recognised as component sources.
var componentExtensions = map[string]bool{
	".tsx":    true,
	".ts":     true,
	".jsx":    true,
	".js":     true,
	".svelte": true,
}

// ProjectConfig represents the parsed representation of revelt.json.
type ProjectConfig struct {
	// Framework defines the UI library used ("react" or "svelte").
	Framework string `json:"framework"`

	// SourceDir is the path containing the front-end source files and components.
	SourceDir string `json:"source_dir"`

	// OutDir is the target destination for the compiled build outputs.
	OutDir string `json:"out_dir"`

	// Workers defines how many Node.js process instances are initialized in the pool.
	Workers int `json:"workers"`

	// TimeoutMS is the render timeout ceiling in milliseconds.
	TimeoutMS int `json:"timeout_ms"`

	// Port defines the default HTTP port to host the server application.
	Port int `json:"port"`

	// StaticPrefix specifies the routing prefix for serving client hydration assets.
	StaticPrefix string `json:"static_prefix"`

	// ComponentDir is the directory name (relative to SourceDir) containing components.
	ComponentDir string `json:"component_dir"`

	// GoBuildCmd is the command to build the go project.
	// Default is "go build"
	GoBuildCmd string `json:"go_build_cmd"`

	// ComponentModes maps each discovered component name to its execution mode.
	// Populated by LoadConfig via filesystem discovery; not present in JSON.
	ComponentModes map[string]string `json:"-"`
}

// LoadConfig reads, parses, and validates an revelt.json configuration file.
// After parsing, it discovers components from the configured component directory
// and populates ProjectConfig.ComponentModes by reading @mode annotations,
// mirroring the discovery logic in build.mjs.
func LoadConfig(path string) (*ProjectConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg ProjectConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config JSON: %w", err)
	}

	if cfg.SourceDir == "" {
		cfg.SourceDir = "./frontend"
	}
	if cfg.OutDir == "" {
		cfg.OutDir = "./dist"
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	if cfg.Port <= 0 {
		cfg.Port = 8080
	}
	if cfg.StaticPrefix == "" {
		cfg.StaticPrefix = "/static/"
	}
	if cfg.ComponentDir == "" {
		cfg.ComponentDir = "src/components"
	}

	cfg.ComponentModes, err = discoverComponentModes(cfg.SourceDir, cfg.ComponentDir)
	if err != nil {
		return nil, fmt.Errorf("discovering components: %w", err)
	}

	return &cfg, nil
}

// discoverComponentModes scans the component directory and returns a map from
// component name (filename stem) to its declared rendering mode. Files without
// a @mode annotation default to ModeHydrate, matching build.mjs behaviour.
func discoverComponentModes(sourceDir, componentDir string) (map[string]string, error) {
	dir := filepath.Join(sourceDir, componentDir)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			// An absent directory is not fatal; the build step will catch it.
			return make(map[string]string), nil
		}
		return nil, fmt.Errorf("reading component directory %q: %w", dir, err)
	}

	modes := make(map[string]string, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := filepath.Ext(e.Name())
		if !componentExtensions[ext] {
			continue
		}

		stem := strings.TrimSuffix(e.Name(), ext)
		absPath := filepath.Join(dir, e.Name())

		mode, err := readModeAnnotation(absPath)
		if err != nil {
			return nil, fmt.Errorf("reading annotation for %q: %w", absPath, err)
		}
		modes[stem] = mode
	}

	return modes, nil
}

// readModeAnnotation scans the first few lines of a component source file for
// a @mode <ssr|hydrate|client|lazy-client> annotation. Returns ModeHydrate when absent.
func readModeAnnotation(path string) (string, error) {
	const searchLines = 5

	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("opening %q: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for i := 0; i < searchLines && scanner.Scan(); i++ {
		line := scanner.Text()
		if mode, ok := extractMode(line); ok {
			return mode, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scanning %q: %w", path, err)
	}

	return ModeHydrate, nil
}

// extractMode parses a single source line for a @mode annotation.
// Returns the mode string and true when a valid annotation is found.
// ModeLazyClient ("lazy-client") must be tested before ModeClient ("client")
// because ModeClient is a prefix of ModeLazyClient.
func extractMode(line string) (string, bool) {
	idx := strings.Index(line, "@mode")
	if idx == -1 {
		return "", false
	}

	rest := strings.TrimSpace(line[idx+len("@mode"):])
	switch {
	case strings.HasPrefix(rest, ModeSSR):
		return ModeSSR, true
	case strings.HasPrefix(rest, ModeHydrate):
		return ModeHydrate, true
	// ModeLazyClient must be checked before ModeClient: "lazy-client" starts
	// with neither "client" nor a unique prefix, but "client" IS a prefix of
	// "lazy-client" only when read in reverse — actually they are distinct
	// prefixes. Regardless, checking the longer token first is defensive.
	case strings.HasPrefix(rest, ModeLazyClient):
		return ModeLazyClient, true
	case strings.HasPrefix(rest, ModeClient):
		return ModeClient, true
	default:
		return "", false
	}
}
