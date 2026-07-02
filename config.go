// Package revelt provides infrastructure for server-side rendering Svelte
// and React components via supervised Node.js processes. This file handles
// loading and validating global framework configurations.
package revelt

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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

// LoadConfig reads, parses, and validates a revelt.json configuration file from disk.
// After parsing, it discovers components from the configured component directory
// and populates ProjectConfig.ComponentModes by reading @mode annotations,
// mirroring the discovery logic in build.mjs.
func LoadConfig(path string) (*ProjectConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}
	return LoadConfigFromBytes(data)
}

// LoadConfigFromBytes parses and validates an already-loaded revelt.json
// payload, scanning the host operating system filesystem for component modes.
// This is the default entry point used by generated main.go templates during local development.
func LoadConfigFromBytes(data []byte) (*ProjectConfig, error) {
	return LoadConfigFromFS(os.DirFS("."), data)
}

// LoadConfigFromFS parses and validates a configuration payload from bytes,
// using the provided filesystem to discover component modes. This allows
// fully self-contained binaries to carry both their configuration and their
// component source metadata in memory.
//
// Safe for concurrent use by multiple goroutines.
func LoadConfigFromFS(sysFS fs.FS, data []byte) (*ProjectConfig, error) {
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

	// Format a valid relative path compliant with fs.FS path specifications (no leading "./").
	cleanPath := filepath.ToSlash(filepath.Clean(filepath.Join(cfg.SourceDir, cfg.ComponentDir)))
	cleanPath = strings.TrimPrefix(cleanPath, "./")

	var err error
	cfg.ComponentModes, err = discoverComponentModes(sysFS, cleanPath)
	if err != nil {
		return nil, fmt.Errorf("discovering components: %w", err)
	}

	return &cfg, nil
}

// discoverComponentModes scans the component directory in the provided filesystem
// and returns a map from component name (filename stem) to its declared rendering mode.
// Files without a @mode annotation default to ModeHydrate, matching build.mjs behaviour.
// discoverComponentModes recursively walks the component directory in the provided filesystem
// and returns a map from relative component paths (e.g. "admin/Header") to their declared rendering modes.
// Files without a @mode annotation default to ModeHydrate.
func discoverComponentModes(sysFS fs.FS, componentDir string) (map[string]string, error) {
	modes := make(map[string]string)

	err := fs.WalkDir(sysFS, componentDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// If the root component folder does not exist, return an empty map gracefully.
			if path == componentDir && (os.IsNotExist(err) || errors.Is(err, fs.ErrNotExist)) {
				return nil
			}
			return err
		}

		// Skip directories themselves; focus only on component source files.
		if d.IsDir() {
			return nil
		}

		ext := filepath.Ext(d.Name())
		if !componentExtensions[ext] {
			return nil
		}

		// Calculate the component path relative to the root component directory.
		// For example, "frontend/src/components/admin/Header.tsx" relative to
		// "frontend/src/components" becomes "admin/Header.tsx".
		relPath, err := filepath.Rel(componentDir, path)
		if err != nil {
			return err
		}

		// Trim file extensions and enforce forward slashes for clean runtime registry keys.
		relStem := strings.TrimSuffix(relPath, ext)
		relStem = filepath.ToSlash(relStem)

		mode, err := readModeAnnotation(sysFS, path)
		if err != nil {
			return fmt.Errorf("reading annotation for %q: %w", path, err)
		}

		modes[relStem] = mode
		return nil
	})

	if err != nil {
		return nil, err
	}

	return modes, nil
}

// readModeAnnotation scans the first few lines of a component source file in the
// provided filesystem for a @mode annotation. Returns ModeHydrate when absent.
func readModeAnnotation(sysFS fs.FS, path string) (string, error) {
	const searchLines = 5

	f, err := sysFS.Open(path)
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
	_, after, ok := strings.Cut(line, "@mode")
	if !ok {
		return "", false
	}

	rest := strings.TrimSpace(after)
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
