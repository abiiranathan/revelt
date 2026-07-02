package gzip

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abiiranathan/revelt"
)

// helloHandler writes a fixed plain-text response.
func helloHandler(w http.ResponseWriter, _ *http.Request) error {
	_, err := w.Write([]byte("Hello, World!"))
	return err
}

func TestGzip_WithAcceptEncoding(t *testing.T) {
	handler := revelt.Adapt(Gzip()(helloHandler), revelt.DefaultErrorHandler)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("expected Content-Encoding: gzip, got %s", w.Header().Get("Content-Encoding"))
	}
	if w.Header().Get("Content-Length") != "" {
		t.Error("Content-Length header should be removed for compressed content")
	}

	reader, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	defer reader.Close()

	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to decompress response: %v", err)
	}

	if string(decompressed) != "Hello, World!" {
		t.Errorf("expected 'Hello, World!', got %s", string(decompressed))
	}
}

func TestGzip_WithoutAcceptEncoding(t *testing.T) {
	handler := revelt.Adapt(Gzip()(helloHandler), revelt.DefaultErrorHandler)

	req := httptest.NewRequest("GET", "/test", nil)
	// No Accept-Encoding header.
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if w.Header().Get("Content-Encoding") != "" {
		t.Errorf("Content-Encoding should not be set, got %s", w.Header().Get("Content-Encoding"))
	}
	if w.Body.String() != "Hello, World!" {
		t.Errorf("expected 'Hello, World!', got %s", w.Body.String())
	}
}

func TestGzip_WithoutGzipInAcceptEncoding(t *testing.T) {
	handler := revelt.Adapt(Gzip()(helloHandler), revelt.DefaultErrorHandler)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Accept-Encoding", "deflate, br")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if w.Header().Get("Content-Encoding") != "" {
		t.Errorf("Content-Encoding should not be set, got %s", w.Header().Get("Content-Encoding"))
	}
	if w.Body.String() != "Hello, World!" {
		t.Errorf("expected 'Hello, World!', got %s", w.Body.String())
	}
}

func TestGzip_SkipPaths(t *testing.T) {
	rawHandler := revelt.Adapt(Gzip("/api/raw", "/static")(func(w http.ResponseWriter, _ *http.Request) error {
		_, err := w.Write([]byte("Raw data"))
		return err
	}), revelt.DefaultErrorHandler)

	compressedHandler := revelt.Adapt(Gzip("/api/raw", "/static")(func(w http.ResponseWriter, _ *http.Request) error {
		_, err := w.Write([]byte("Compressed data"))
		return err
	}), revelt.DefaultErrorHandler)

	// Test skipped path.
	req1 := httptest.NewRequest("GET", "/api/raw/data", nil)
	req1.Header.Set("Accept-Encoding", "gzip")
	w1 := httptest.NewRecorder()
	rawHandler.ServeHTTP(w1, req1)

	if w1.Header().Get("Content-Encoding") != "" {
		t.Errorf("skipped path should not be compressed, got Content-Encoding: %s", w1.Header().Get("Content-Encoding"))
	}
	if w1.Body.String() != "Raw data" {
		t.Errorf("expected 'Raw data', got %s", w1.Body.String())
	}

	// Test non-skipped path.
	req2 := httptest.NewRequest("GET", "/api/compressed", nil)
	req2.Header.Set("Accept-Encoding", "gzip")
	w2 := httptest.NewRecorder()
	compressedHandler.ServeHTTP(w2, req2)

	if w2.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("non-skipped path should be compressed, got Content-Encoding: %s", w2.Header().Get("Content-Encoding"))
	}
}

func TestGzipLevel_BestSpeed(t *testing.T) {
	handler := revelt.Adapt(GzipLevel(gzip.BestSpeed)(func(w http.ResponseWriter, _ *http.Request) error {
		data := strings.Repeat("Hello, World! ", 100)
		_, err := w.Write([]byte(data))
		return err
	}), revelt.DefaultErrorHandler)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("expected Content-Encoding: gzip, got %s", w.Header().Get("Content-Encoding"))
	}

	reader, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	defer reader.Close()

	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to decompress response: %v", err)
	}

	expected := strings.Repeat("Hello, World! ", 100)
	if string(decompressed) != expected {
		t.Errorf("decompressed content doesn't match expected")
	}
}

func TestGzipLevel_BestCompression(t *testing.T) {
	handler := revelt.Adapt(GzipLevel(gzip.BestCompression)(func(w http.ResponseWriter, _ *http.Request) error {
		data := strings.Repeat("This is a test string for compression. ", 50)
		_, err := w.Write([]byte(data))
		return err
	}), revelt.DefaultErrorHandler)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("expected Content-Encoding: gzip, got %s", w.Header().Get("Content-Encoding"))
	}
}

func TestGzip_JSONResponse(t *testing.T) {
	handler := revelt.Adapt(Gzip()(func(w http.ResponseWriter, _ *http.Request) error {
		body := `{"message":"Hello, World!","status":"success","data":[1,2,3,4,5]}`
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(body))
		return err
	}), revelt.DefaultErrorHandler)

	req := httptest.NewRequest("GET", "/json", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("expected Content-Encoding: gzip, got %s", w.Header().Get("Content-Encoding"))
	}

	reader, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	defer reader.Close()

	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to decompress response: %v", err)
	}

	if !strings.Contains(string(decompressed), `"message":"Hello, World!"`) {
		t.Error("decompressed JSON doesn't contain expected content")
	}
}

func TestGzip_LargeResponse(t *testing.T) {
	handler := revelt.Adapt(Gzip()(func(w http.ResponseWriter, _ *http.Request) error {
		data := strings.Repeat("This is a large response for testing compression efficiency. ", 200)
		_, err := w.Write([]byte(data))
		return err
	}), revelt.DefaultErrorHandler)

	req := httptest.NewRequest("GET", "/large", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	originalSize := len(strings.Repeat("This is a large response for testing compression efficiency. ", 200))
	compressedSize := w.Body.Len()

	if compressedSize >= originalSize {
		t.Errorf("compressed size (%d) should be smaller than original size (%d)", compressedSize, originalSize)
	}

	reader, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	defer reader.Close()

	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to decompress response: %v", err)
	}

	if len(decompressed) != originalSize {
		t.Errorf("decompressed size (%d) doesn't match original size (%d)", len(decompressed), originalSize)
	}
}
