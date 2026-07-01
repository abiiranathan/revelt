package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	watchInterval     = 500 * time.Millisecond
	goRestartDebounce = 300 * time.Millisecond
)

// devRunner coordinates the Node.js file watcher and a self-restarting Go
// process. It blocks until the parent context is cancelled (SIGINT/SIGTERM).
type devRunner struct {
	sourceDir string // frontend source directory (from revelt.json)
	clientDir string // Output path for all client files in dist e.g "frontend/dist/client"
	goArgs    []string
}

func runDev(ctx context.Context, sourceDir string, clientOutDir string) {
	r := &devRunner{
		sourceDir: sourceDir,
		clientDir: clientOutDir,
		goArgs:    []string{"run", "."},
	}
	r.run(ctx)
}

func (r *devRunner) run(ctx context.Context) {
	// Start the Node watcher first — it takes a second to initialise.
	nodeCtx, nodeCancel := context.WithCancel(ctx)
	defer nodeCancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		r.runNodeWatcher(nodeCtx)
	}()

	// Watch changes in frontend/dist/client to restart go application since we are embedding assets
	// =============================================================================================
	restart := make(chan struct{}, 2) // watched by 2 goroutines
	wg.Add(1)
	go func() {
		defer wg.Done()
		r.watchFrontEndForChanges(ctx, restart)
	}()

	// Run and auto-restart the Go server whenever .go files change.
	r.runGoWithRestart(ctx, restart)

	nodeCancel()
	wg.Wait()
}

// runNodeWatcher starts `node build.mjs --watch` inside sourceDir and lets it
// run until the context is cancelled.
func (r *devRunner) runNodeWatcher(ctx context.Context) {
	for {
		cmd := exec.CommandContext(ctx, "node", "build.mjs", "--watch")
		cmd.Dir = r.sourceDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Start(); err != nil {
			log.Printf("[revelt] node watcher failed to start: %v", err)
			return
		}

		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()

		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			<-done
			return
		case err := <-done:
			if ctx.Err() != nil {
				return
			}
			log.Printf("[revelt] node watcher exited (%v); restarting…", err)
			time.Sleep(1 * time.Second)
		}
	}
}

func (r *devRunner) watchFrontEndForChanges(ctx context.Context, restart chan struct{}) {
	matchClientAsset := func(path string, d fs.DirEntry) bool {
		switch filepath.Ext(path) {
		case ".js", ".css", ".html", ".map":
			return true
		}
		return false
	}

	hashes := collectHashes(r.clientDir, matchClientAsset)
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()

	var pendingRestart bool
	var lastChange time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if changed, current := hasFileChangedByHash(r.clientDir, hashes, matchClientAsset); changed {
				hashes = current
				pendingRestart = true
				lastChange = time.Now()
			}

			if pendingRestart && time.Since(lastChange) >= goRestartDebounce {
				select {
				case restart <- struct{}{}:
				default:
				}
				pendingRestart = false
			}
		}
	}
}

// runGoWithRestart watches .go and config files, compiles, and restarts the Go process.
func (r *devRunner) runGoWithRestart(ctx context.Context, restart chan struct{}) {
	var (
		cmd           *exec.Cmd
		processExited = make(chan error, 1)
		binName       = "./revelt_bin"
	)
	if runtime.GOOS == "windows" {
		binName = `.\revelt_bin.exe`
	}

	// Helper to cleanly kill and wait for the running process to stop.
	killProcess := func() {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			<-processExited
			cmd = nil
		}
	}

	// File watcher goroutine.
	matchGoSource := func(path string, d fs.DirEntry) bool {
		return filepath.Ext(path) == ".go" || d.Name() == "revelt.json"
	}

	go func() {
		mtimes := collectMtimes(".", matchGoSource)
		ticker := time.NewTicker(watchInterval)
		defer ticker.Stop()

		var pendingRestart bool
		var lastChange time.Time

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if changed, current := hasFileChanged(".", mtimes, matchGoSource); changed {
					mtimes = current
					pendingRestart = true
					lastChange = time.Now()
				}

				if pendingRestart && time.Since(lastChange) >= goRestartDebounce {
					select {
					case restart <- struct{}{}:
					default:
					}
					pendingRestart = false
				}
			}
		}
	}()

	// Trigger initial compile and run.
	select {
	case restart <- struct{}{}:
	default:
	}

	for {
		select {
		case <-ctx.Done():
			killProcess()
			_ = os.Remove(binName)
			return

		case err := <-processExited:
			cmd = nil
			if ctx.Err() == nil {
				log.Printf("[revelt] go server exited (%v); restarting…", err)
				time.Sleep(1 * time.Second)
				select {
				case restart <- struct{}{}:
				default:
				}
			}

		case <-restart:
			killProcess()

			fmt.Println("[revelt] compiling Go server…")
			buildCmd := exec.CommandContext(ctx, "go", "build", "-o", binName, ".")
			buildCmd.Stdout = os.Stdout
			buildCmd.Stderr = os.Stderr
			if err := buildCmd.Run(); err != nil {
				log.Printf("[revelt] build failed: %v", err)
				continue
			}

			fmt.Println("[revelt] starting Go server…")
			cmd = exec.Command(binName)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Start(); err != nil {
				log.Printf("[revelt] failed to start Go server: %v", err)
				continue
			}

			go func(c *exec.Cmd) {
				processExited <- c.Wait()
			}(cmd)
		}
	}
}

// collectMtimes returns a map of path → mtime for files under root that
// satisfy match. Hidden directories and common build/dependency folders
// are always skipped.
func collectMtimes(root string, match func(path string, d fs.DirEntry) bool) map[string]time.Time {
	mtimes := make(map[string]time.Time, 64)
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		name := d.Name()

		if d.IsDir() {
			if path == root {
				return nil
			}
			if strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			switch name {
			case "node_modules", "dist", "build", "bin", "tmp", "temp", "vendor", "coverage":
				return filepath.SkipDir
			}
			return nil
		}

		if match(path, d) {
			info, err := d.Info()
			if err == nil {
				mtimes[path] = info.ModTime()
			}
		}
		return nil
	})
	return mtimes
}

// hasFileChanged compares the current file mtimes under root against the
// previously recorded snapshot.
func hasFileChanged(root string, prev map[string]time.Time, match func(path string, d fs.DirEntry) bool) (bool, map[string]time.Time) {
	current := collectMtimes(root, match)
	if len(current) != len(prev) {
		return true, current
	}
	for path, mtime := range current {
		if prev[path] != mtime {
			return true, current
		}
	}
	return false, current
}

// collectHashes returns a map of path → content hash (SHA-256) for files
// under root that satisfy match. Used instead of mtime comparison for
// directories where files may be rewritten with identical content on every
// build (e.g. Vite's asset injection), which would otherwise cause false
// positives on plain mtime checks.
func collectHashes(root string, match func(path string, d fs.DirEntry) bool) map[string][32]byte {
	hashes := make(map[string][32]byte, 64)
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		name := d.Name()

		if d.IsDir() {
			if path == root {
				return nil
			}
			if strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			switch name {
			case "node_modules", "dist", "build", "bin", "tmp", "temp", "vendor", "coverage":
				return filepath.SkipDir
			}
			return nil
		}

		if match(path, d) {
			data, err := os.ReadFile(path)
			if err == nil {
				hashes[path] = sha256.Sum256(data)
			}
		}
		return nil
	})
	return hashes
}

// hasFileChangedByHash compares the current file content hashes under root
// against the previously recorded snapshot.
func hasFileChangedByHash(root string, prev map[string][32]byte, match func(path string, d fs.DirEntry) bool) (bool, map[string][32]byte) {
	current := collectHashes(root, match)
	if len(current) != len(prev) {
		return true, current
	}
	for path, hash := range current {
		if prev[path] != hash {
			return true, current
		}
	}
	return false, current
}
