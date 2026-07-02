package revelt_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/abiiranathan/revelt"
)

// echoServer writes a temporary CommonJS harness to simulate the Node subprocess interface.
func echoServer(t testing.TB) string {
	t.Helper()

	const script = `
const readline = require('readline');
const rl = readline.createInterface({ input: process.stdin });

rl.on('line', line => {
    let req;
    try { req = JSON.parse(line); } catch(e) {
        process.stdout.write(JSON.stringify({ id: 0, error: e.message }) + '\n');
        return;
    }
    const html = '<div data-component="' + req.component + '"></div>';
    process.stdout.write(JSON.stringify({ id: req.id, html }) + '\n');
});

rl.on('close', () => process.exit(0));
`

	f, err := os.CreateTemp(t.TempDir(), "render-server-*.cjs")
	if err != nil {
		t.Fatalf("creating temp script: %v", err)
	}
	if _, err := fmt.Fprint(f, script); err != nil {
		t.Fatalf("writing temp script: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing temp script: %v", err)
	}
	return f.Name()
}

// TestRenderBasic ensures standard server rendering returns expected HTML and headers.
func TestRenderBasic(t *testing.T) {
	t.Parallel()

	script := echoServer(t)
	ctx := context.Background()

	r, err := revelt.New(ctx, script, revelt.WithWorkers(1))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	out, err := r.Render(ctx, revelt.RenderInput{
		Component: "Counter",
		Props:     map[string]any{"initial": 0},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	wantHTML := `<div data-component="Counter"></div>`
	if out.HTML != wantHTML {
		t.Errorf("HTML: got %q, want %q", out.HTML, wantHTML)
	}

}

// TestClientOnlyRender ensures that client-only configurations bypass the Node engine.
func TestClientOnlyRender(t *testing.T) {
	t.Parallel()

	script := echoServer(t)
	ctx := context.Background()

	cfg := &revelt.ProjectConfig{
		ComponentModes: map[string]string{
			"MyClientWidget": revelt.ModeClient,
		},
	}

	r, err := revelt.New(ctx, script,
		revelt.WithWorkers(1),
		revelt.WithProjectConfig(cfg),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	out, err := r.Render(ctx, revelt.RenderInput{
		Component: "MyClientWidget",
		Props: map[string]any{
			"theme": "dark",
		},
	})
	if err != nil {
		t.Fatalf("Render client-only component: %v", err)
	}

	const want = `<div data-ssr-island="MyClientWidget" data-ssr-props="{&quot;theme&quot;:&quot;dark&quot;}"></div>`
	if out.HTML != want {
		t.Errorf("HTML:\n got  %q\n want %q", out.HTML, want)
	}
}
