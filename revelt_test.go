package revelt_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abiiranathan/revelt"
)

func TestFullIntegration_Workflow(t *testing.T) {
	// 1. Setup a realistic temporary directory structure
	tmpDir := t.TempDir()

	// Change the working directory of the test to tmpDir using Go 1.24's t.Chdir
	t.Chdir(tmpDir)

	sourceDir := "frontend"
	compDir := filepath.Join(sourceDir, "src", "components")

	if err := os.MkdirAll(compDir, 0755); err != nil {
		t.Fatalf("failed to construct test directories: %v", err)
	}

	// 2. Write simulated components with annotations
	counterPath := filepath.Join(compDir, "Counter.tsx")
	counterContent := `// @mode hydrate
export default function Counter() { return <div>Count</div>; }
`
	if err := os.WriteFile(counterPath, []byte(counterContent), 0644); err != nil {
		t.Fatalf("failed to write Counter component: %v", err)
	}

	chartPath := filepath.Join(compDir, "ClientChart.tsx")
	chartContent := `// @mode lazy-client
export default function ClientChart() { return <div>Chart</div>; }
`
	if err := os.WriteFile(chartPath, []byte(chartContent), 0644); err != nil {
		t.Fatalf("failed to write ClientChart component: %v", err)
	}

	// 3. Write revelt.json config with standard relative paths
	configJSON := `{
		"framework": "react",
		"source_dir": "frontend",
		"out_dir": "frontend/dist",
		"workers": 1,
		"timeout_ms": 100,
		"port": 9090,
		"static_prefix": "/static/",
		"component_dir": "src/components"
	}`

	if err := os.WriteFile("revelt.json", []byte(configJSON), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// Write mock client index.html to serve as our template
	clientDistDir := filepath.Join(sourceDir, "dist", "client")
	if err := os.MkdirAll(clientDistDir, 0755); err != nil {
		t.Fatalf("failed to make client dist directory: %v", err)
	}

	indexHTMLContent := `<!DOCTYPE html><html><head></head><body>{{ .Main }}</body></html>`
	if err := os.WriteFile(filepath.Join(clientDistDir, "index.html"), []byte(indexHTMLContent), 0644); err != nil {
		t.Fatalf("failed to write placeholder index.html: %v", err)
	}

	// 4. Load config and verify correct filesystem discovery of component modes
	cfg, err := revelt.LoadConfig("revelt.json")
	if err != nil {
		t.Fatalf("failed to parse config path: %v", err)
	}

	if mode, exists := cfg.ComponentModes["Counter"]; !exists || mode != revelt.ModeHydrate {
		t.Errorf("expected Counter component mode to be 'hydrate', got %s", mode)
	}

	if mode, exists := cfg.ComponentModes["ClientChart"]; !exists || mode != revelt.ModeLazyClient {
		t.Errorf("expected ClientChart component mode to be 'lazy-client', got %s", mode)
	}

	// 5. Setup mocked renderer and App
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Write a mock empty render-server binary to satisfy sidecar executable check
	sidecarPath := filepath.Join(sourceDir, "dist", "render-server.cjs")
	script := `const readline = require('readline');
const rl = readline.createInterface({ input: process.stdin });
rl.on('line', line => {
	const req = JSON.parse(line);
	process.stdout.write(JSON.stringify({ id: req.id, html: "<div>mocked</div>" }) + "\n");
});
rl.on('close', () => process.exit(0));`

	if err := os.WriteFile(sidecarPath, []byte(script), 0644); err != nil {
		t.Fatalf("failed to write mock sidecar: %v", err)
	}

	staticFS := http.Dir(clientDistDir)
	app, err := revelt.NewAppFromConfig(ctx, cfg, staticFS, sidecarPath)
	if err != nil {
		t.Fatalf("failed to create App: %v", err)
	}

	// 6. Send simulated request through the handler directly to verify the bypass
	req := httptest.NewRequest("GET", "/test-page", nil)
	rec := httptest.NewRecorder()

	handler := http.HandlerFunc(func(rw http.ResponseWriter, rq *http.Request) {
		err := app.NewPage().
			Slot("Main", "ClientChart", map[string]any{"data": []int{1, 2, 3}}).
			Render(rq.Context(), rw)
		if err != nil {
			t.Errorf("failed to render test page: %v", err)
		}
	})

	handler.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Errorf("expected OK status, got %d", res.StatusCode)
	}

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}
	bodyStr := string(bodyBytes)

	expectedContainer := `data-ssr-island="ClientChart"`
	expectedProps := `data-ssr-props="{&quot;data&quot;:[1,2,3]}"`
	expectedLazy := `data-ssr-lazy="true"`

	if !strings.Contains(bodyStr, expectedContainer) {
		t.Errorf("missing island name in output. Got body:\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, expectedProps) {
		t.Errorf("missing escaped props in output. Got body:\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, expectedLazy) {
		t.Errorf("missing lazy-client trigger attribute in output. Got body:\n%s", bodyStr)
	}
}
