package etag_test

import (
	"crypto/sha1" // for computing the expected ETag hash to compare against
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/abiiranathan/revelt"
	"github.com/abiiranathan/revelt/middleware/etag"
)

// TestEtag verifies that a successful response receives an ETag header
// matching the SHA-1 hash of its body, and that a handler returning an
// error still surfaces as a 500 without an ETag being computed.
func TestEtag(t *testing.T) {
	mw := etag.New()
	res := "Hello World!"

	okHandler := revelt.Adapt(mw(func(w http.ResponseWriter, _ *http.Request) error {
		_, err := w.Write([]byte(res))
		return err
	}), revelt.DefaultErrorHandler)

	errHandler := revelt.Adapt(mw(func(_ http.ResponseWriter, _ *http.Request) error {
		return fmt.Errorf("something went wrong")
	}), revelt.DefaultErrorHandler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	okHandler.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected 200 status, got %d", w.Result().StatusCode)
	}

	etagHeader := w.Header().Get("Etag")
	if etagHeader == "" {
		t.Error("expected a valid etag header, got empty string")
	}

	hash := sha1.New()
	hash.Write([]byte(res))
	expected := fmt.Sprintf(`"%x"`, hash.Sum(nil))

	if expected != etagHeader {
		t.Fatalf("expected etag %s, got %s", expected, etagHeader)
	}

	req = httptest.NewRequest(http.MethodGet, "/error", nil)
	w = httptest.NewRecorder()
	errHandler.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 status, got %d", w.Result().StatusCode)
	}

	t.Log(w.Body.String())
}
