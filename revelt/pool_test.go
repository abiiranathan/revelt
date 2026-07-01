package revelt

import (
	"context"
	"os"
	"testing"

	"github.com/abiiranathan/revelt/protocol"
)

// mockNodeServer writes a simple Node.js pipeline script that behaves like
// the real sidecar but requires no external dependencies.
func mockNodeServer(t *testing.T) string {
	t.Helper()
	script := `
const readline = require('readline');
const rl = readline.createInterface({ input: process.stdin });
rl.on('line', line => {
    try {
        const req = JSON.parse(line);
        process.stdout.write(JSON.stringify({ id: req.id, html: "<div>mocked</div>" }) + "\n");
    } catch(err) {
        process.stdout.write(JSON.stringify({ id: 0, error: err.message }) + "\n");
    }
});
rl.on('close', () => process.exit(0));
`
	tmpFile, err := os.CreateTemp(t.TempDir(), "mock-server-*.cjs")
	if err != nil {
		t.Fatalf("failed to create temporary mock node file: %v", err)
	}
	if _, err := tmpFile.WriteString(script); err != nil {
		t.Fatalf("failed to write mock node script: %v", err)
	}
	tmpFile.Close()
	return tmpFile.Name()
}

func TestPool_AutomaticWorkerRecovery(t *testing.T) {
	scriptPath := mockNodeServer(t)
	ctx := context.Background()

	cfg := workerConfig{
		NodeBin:     "node",
		ReadBufSize: 4096,
	}

	p, err := newPool(ctx, scriptPath, 2, cfg)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer p.close()

	// Capture initial processes
	w0 := p.workers[0]
	if !w0.alive() {
		t.Fatal("expected worker 0 to be alive initially")
	}

	// Forcibly kill the first worker's process to simulate a crash
	if err := w0.cmd.Process.Kill(); err != nil {
		t.Fatalf("failed to kill process for testing: %v", err)
	}

	// Verify the pool transparently recovers the worker and handles the incoming request
	req := protocol.RenderRequest{
		Component: "Counter",
		Props:     nil,
	}

	// Perform a render that might route to worker 0 or 1.
	// We run multiple times to guarantee the round-robin hits the killed slot.
	for range 4 {
		resp, err := p.render(ctx, req)
		if err != nil {
			t.Fatalf("render call failed after worker crash: %v", err)
		}
		if resp.HTML != "<div>mocked</div>" {
			t.Errorf("unexpected response HTML: %s", resp.HTML)
		}
	}

	// Verify that the slot previously occupied by the killed process is now occupied by a healthy worker
	stats := p.stats()
	for idx, stat := range stats {
		if !stat.Alive {
			t.Errorf("worker %d is not alive after recovery cycle", idx)
		}
	}
}
