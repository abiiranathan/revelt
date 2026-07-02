package cors_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/abiiranathan/revelt"
	"github.com/abiiranathan/revelt/middleware/cors"
)

// TestCors verifies that a request from an allowed origin receives the
// expected Access-Control-Allow-Origin header, and that a request from an
// origin outside the allow-list is rejected with 403 Forbidden.
func TestCors(t *testing.T) {
	host := "localhost:8080"

	mw := cors.New(cors.CORSOptions{
		AllowCredentials: true,
		Allowwebsockets:  true,
		AllowedOrigins:   []string{host},
		AllowedMethods:   []string{"OPTIONS", "GET"},
		AllowedHeaders:   []string{"Content-Type", "Host"},
		ExposedHeaders:   []string{"Content-Length", "Cache-Control"},
		MaxAge:           3600,
	})

	res := "Hello World"
	handler := revelt.Adapt(mw(func(w http.ResponseWriter, _ *http.Request) error {
		_, err := w.Write([]byte(res))
		return err
	}), revelt.DefaultErrorHandler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", host)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected 200 status, got %d", w.Result().StatusCode)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != host {
		t.Errorf("expected Access-Control-Allow-Origin in response headers")
	}

	// Test with unsupported origin.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "localhost:3030")
	w = httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 status, got %d", w.Result().StatusCode)
	}
}
