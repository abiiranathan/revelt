package main

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
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

// runGoWithRestart watches .go files under the current directory and restarts
// `go run .` whenever they change. Blocks until ctx is cancelled.
func (r *devRunner) runGoWithRestart(ctx context.Context) {
	var (
		goProc  *exec.Cmd
		procMu  sync.Mutex
		restart = make(chan struct{}, 1)
	)

	triggerRestart := func() {
		select {
		case restart <- struct{}{}:
		default: // a restart is already queued
		}
	}

	// File watcher goroutine.
	go func() {
		mtimes := collectMtimes(".")
		ticker := time.NewTicker(goWatchInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if changed, _ := hasGoFileChanged(".", mtimes); changed {
					mtimes = collectMtimes(".")
					triggerRestart()
				}
			}
		}
	}()

	// Initial start.
	triggerRestart()

	debounce := time.NewTimer(0)
	<-debounce.C // drain the immediately-firing timer

	for {
		select {
		case <-ctx.Done():
			procMu.Lock()
			if goProc != nil && goProc.Process != nil {
				_ = goProc.Process.Kill()
			}
			procMu.Unlock()
			return

		case <-restart:
			// Debounce: if another change arrives within 300 ms, reset the timer.
			debounce.Reset(goRestartDebounce)
			// Drain any further buffered restart signals during the debounce window.
		drainLoop:
			for {
				select {
				case <-restart:
					debounce.Reset(goRestartDebounce)
				case <-debounce.C:
					break drainLoop
				}
			}

			procMu.Lock()
			if goProc != nil && goProc.Process != nil {
				fmt.Println("[revelt] restarting Go server…")
				_ = goProc.Process.Kill()
				_ = goProc.Wait()
			}

			cmd := exec.CommandContext(ctx, "go", r.goArgs...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Start(); err != nil {
				log.Printf("[revelt] go run failed: %v", err)
				procMu.Unlock()
				continue
			}
			goProc = cmd
			procMu.Unlock()

			// Let the process run in the background; we don't Wait here because
			// we kill it ourselves on the next restart signal or context cancel.
			go func(c *exec.Cmd) {
				if err := c.Wait(); err != nil && ctx.Err() == nil {
					log.Printf("[revelt] go server exited: %v", err)
					// Trigger a restart so a crash causes a re-run after the
					// next file save.
					triggerRestart()
				}
			}(cmd)
		}
	}
}

// collectMtimes returns a map of path → mtime for all .go files under root.
func collectMtimes(root string) map[string]time.Time {
	mtimes := make(map[string]time.Time, 64)
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		name := d.Name()

		switch name {
		case ".git", ".hg", ".svn",
			".idea", ".vscode", ".cache", ".direnv",
			"node_modules",
			"dist", "build", "bin",
			"tmp", "temp",
			"vendor",
			"coverage":
			return filepath.SkipDir
		}

		if strings.HasPrefix(name, ".") {
			return filepath.SkipDir
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		mtimes[path] = info.ModTime()
		return nil
	})
	return mtimes
}

// hasGoFileChanged compares the current file mtimes under root against the
// previously recorded snapshot. Returns true if any file was added, removed,
// or modified.
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
