package logger

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abiiranathan/revelt"
)

// setupHandler builds a logging-wrapped "/hello" handler writing its
// output to buf, returning the handler ready to serve requests.
func setupHandler(t *testing.T, cfg *Config) (http.HandlerFunc, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	if cfg == nil {
		cfg = &Config{}
	}
	cfg.Output = &buf

	handler := revelt.Adapt(New(cfg)(func(w http.ResponseWriter, _ *http.Request) error {
		_, err := w.Write([]byte("ok"))
		return err
	}), revelt.DefaultErrorHandler)
	return handler.ServeHTTP, &buf
}

func TestLogger_TextFormat_Basic(t *testing.T) {
	handler, buf := setupHandler(t, &Config{Format: TextFormat})
	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", w.Code)
	}
	out := buf.String()
	if !strings.Contains(out, "status=") || !strings.Contains(out, "method=") || !strings.Contains(out, "path=") {
		t.Fatalf("expected basic keys in text log, got: %s", out)
	}
	t.Log(out)
}

func TestLogger_JSONFormat_Basic(t *testing.T) {
	handler, buf := setupHandler(t, &Config{Format: JSONFormat})
	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", w.Code)
	}

	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	var entry map[string]any
	if err := dec.Decode(&entry); err != nil {
		t.Fatalf("expected JSON log, decode error: %v; raw: %s", err, buf.String())
	}

	t.Log(entry)
	if int(entry["status"].(float64)) != http.StatusOK {
		t.Fatalf("expected status %d, got %v", http.StatusOK, entry["status"])
	}
	if entry["method"] != http.MethodGet {
		t.Fatalf("expected method %s, got %s", http.MethodGet, entry["method"])
	}
	if entry["path"] != "/hello" {
		t.Fatalf("expected path %s, got %s", "/hello", entry["path"])
	}
	if entry["latency"] == "" {
		t.Fatalf("expected latency, got empty")
	}
	if entry["user_agent"] == "" {
		t.Fatalf("expected user_agent, got empty")
	}
	if entry["ip"] == "" {
		t.Fatalf("expected ip, got empty")
	}
}

func TestLogger_SkipPath(t *testing.T) {
	handler, buf := setupHandler(t, &Config{Skip: []string{"/hello"}})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	handler(w, req)

	if buf.Len() != 0 {
		t.Fatalf("expected no logs for skipped path, got: %s", buf.String())
	}
}

func TestLogger_SkipIf(t *testing.T) {
	cfg := &Config{SkipIf: func(r *http.Request) bool { return r.URL.Path == "/hello" }}
	handler, buf := setupHandler(t, cfg)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	handler(w, req)

	if buf.Len() != 0 {
		t.Fatalf("expected no logs for SkipIf, got: %s", buf.String())
	}
}

func TestLogger_Flags_IP_UserAgent_Latency(t *testing.T) {
	cfg := &Config{Format: TextFormat, Flags: LogIP | LogUserAgent | LogLatency}
	handler, buf := setupHandler(t, cfg)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	req.Header.Set("User-Agent", "logger-test")
	req.Header.Set("X-Real-Ip", "203.0.113.9")
	handler(w, req)

	out := buf.String()
	t.Log(out)

	if !strings.Contains(out, "user_agent=logger-test") {
		t.Fatalf("expected user_agent in log, got: %s", out)
	}
	if !strings.Contains(out, "ip=203.0.113.9") {
		t.Fatalf("expected ip in log, got: %s", out)
	}
	if !strings.Contains(out, "latency=") {
		t.Fatalf("expected latency in log, got: %s", out)
	}
}

func TestLogger_Callback_AppendsArgs(t *testing.T) {
	cfg := &Config{Format: TextFormat, Callback: func(_ http.ResponseWriter, _ *http.Request, args ...any) []any {
		return append(args, "request_id", "abc123")
	}}

	handler, buf := setupHandler(t, cfg)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	handler(w, req)

	out := buf.String()
	if !strings.Contains(out, "request_id=abc123") {
		t.Fatalf("expected request_id from callback, got: %s", out)
	}
}
