package brotli_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/abiiranathan/revelt"
	"github.com/abiiranathan/revelt/middleware/brotli"
	"github.com/abiiranathan/revelt/middleware/logger"
	andybrotli "github.com/andybalholm/brotli"
)

// TestBrotliMiddleware verifies that a response is compressed with Brotli
// when the client advertises support for it, and validates that the
// compressed data can be successfully decompressed to the original string.
func TestBrotliMiddleware(t *testing.T) {
	final := func(w http.ResponseWriter, _ *http.Request) error {
		_, err := w.Write([]byte("Hello World"))
		return err
	}

	// Compose logger (outermost) -> brotli -> final, matching the original
	// router.Use(logger.New(nil)); router.Use(brotli.Brotli()) ordering.
	handler := logger.New(nil)(brotli.Brotli()(final))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "br")
	w := httptest.NewRecorder()

	revelt.Adapt(handler, revelt.DefaultErrorHandler).ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}

	if ce := resp.Header.Get("Content-Encoding"); ce != "br" {
		t.Errorf("expected Content-Encoding header %q, got %q", "br", ce)
	}

	// Decompress the response body to verify the content
	reader := andybrotli.NewReader(w.Body)
	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to decompress response body: %v", err)
	}

	expected := "Hello World"
	if string(decompressed) != expected {
		t.Errorf("expected decompressed body %q, got %q", expected, string(decompressed))
	}
}
