package requestid

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/abiiranathan/revelt"
)

// okHandler writes a plain "ok" response.
func okHandler(w http.ResponseWriter, _ *http.Request) error {
	_, err := w.Write([]byte("ok"))
	return err
}

func TestRequestID(t *testing.T) {
	handler := revelt.Adapt(New()(okHandler), revelt.DefaultErrorHandler)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	rid := w.Header().Get(HeaderKey)
	if rid == "" {
		t.Error("expected X-Request-ID header")
	}
	if len(rid) != 32 {
		t.Errorf("expected 32 chars ID, got %d", len(rid))
	}
}

func TestRequestIDConfig(t *testing.T) {
	handler := revelt.Adapt(WithConfig(Config{
		Header: "X-Trace-ID",
		Generator: func() string {
			return "custom-id"
		},
	})(okHandler), revelt.DefaultErrorHandler)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(w, req)

	rid := w.Header().Get("X-Trace-ID")
	if rid != "custom-id" {
		t.Errorf("expected custom-id, got %s", rid)
	}
}

func TestRequestIDExisting(t *testing.T) {
	handler := revelt.Adapt(New()(okHandler), revelt.DefaultErrorHandler)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(HeaderKey, "existing-id")
	handler.ServeHTTP(w, req)

	rid := w.Header().Get(HeaderKey)
	if rid != "existing-id" {
		t.Errorf("expected existing-id, got %s", rid)
	}
}
