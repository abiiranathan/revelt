package main

import (
	"context"
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
	goWatchInterval   = 500 * time.Millisecond
	goRestartDebounce = 300 * time.Millisecond
)

// devRunner coordinates the Node.js file watcher and a self-restarting Go
// process. It blocks until the parent context is cancelled (SIGINT/SIGTERM).
type devRunner struct {
	sourceDir string // frontend source directory (from revelt.json)
	goArgs    []string
}

func runDev(ctx context.Context, sourceDir string) {
	r := &devRunner{
		sourceDir: sourceDir,
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

	// Run and auto-restart the Go server whenever .go files change.
	r.runGoWithRestart(ctx)

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

// runGoWithRestart watches .go and config files, compiles, and restarts the Go process.
func (r *devRunner) runGoWithRestart(ctx context.Context) {
	var (
		cmd           *exec.Cmd
		restart       = make(chan struct{}, 1)
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
	go func() {
		mtimes := collectMtimes(".")
		ticker := time.NewTicker(goWatchInterval)
		defer ticker.Stop()

		var pendingRestart bool
		var lastChange time.Time

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if changed, current := hasGoFileChanged(".", mtimes); changed {
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

// collectMtimes returns a map of path → mtime for all .go files and revelt.json under root.
func collectMtimes(root string) map[string]time.Time {
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
			// Skip hidden directories and common build/dependency folders
			if strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			switch name {
			case "node_modules", "dist", "build", "bin", "tmp", "temp", "vendor", "coverage":
				return filepath.SkipDir
			}
			return nil
		}

		if filepath.Ext(path) == ".go" || name == "revelt.json" {
			info, err := d.Info()
			if err == nil {
				mtimes[path] = info.ModTime()
			}
		}
		return nil
	})
	return mtimes
}

// hasGoFileChanged compares the current file mtimes under root against the
// previously recorded snapshot.
func hasGoFileChanged(root string, prev map[string]time.Time) (bool, map[string]time.Time) {
	current := collectMtimes(root)
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
