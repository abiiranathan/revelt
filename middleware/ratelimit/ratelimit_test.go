package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/abiiranathan/revelt"
)

// okHandler writes a plain "ok" response.
func okHandler(w http.ResponseWriter, _ *http.Request) error {
	_, err := w.Write([]byte("ok"))
	return err
}

func TestRateLimit(t *testing.T) {
	// 5 requests per second, burst 5.
	config := Config{
		Rate:       5,
		Capacity:   5,
		Expiration: time.Minute,
	}

	handler := revelt.Adapt(New(config)(okHandler), revelt.DefaultErrorHandler)

	// Perform 5 allowed requests.
	for i := range 5 {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("request %d failed: %d", i, w.Code)
		}
	}

	// Perform 1 blocked request.
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w.Code)
	}
}

func TestRateLimitRecovery(t *testing.T) {
	// 10 requests per second (1 per 100ms).
	config := Config{
		Rate:     10,
		Capacity: 1,
	}

	handler := revelt.Adapt(New(config)(okHandler), revelt.DefaultErrorHandler)

	// Consume capacity.
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatal("first request failed")
	}

	// Immediate next request should fail (capacity 1 exhausted).
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatal("expected limit reached")
	}

	// Wait for refill (100ms + buffer).
	time.Sleep(150 * time.Millisecond)

	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected recovery, got %d", w.Code)
	}
}

func TestRateLimitCustomKey(t *testing.T) {
	config := Config{
		Rate:     1,
		Capacity: 1,
		KeyFunc: func(r *http.Request) string {
			return r.Header.Get("X-API-Key")
		},
	}

	handler := revelt.Adapt(New(config)(okHandler), revelt.DefaultErrorHandler)

	// Client A.
	reqA := httptest.NewRequest("GET", "/", nil)
	reqA.Header.Set("X-API-Key", "A")
	wA := httptest.NewRecorder()
	handler.ServeHTTP(wA, reqA)
	if wA.Code != http.StatusOK {
		t.Error("client A failed")
	}

	// Client B (should perform independently).
	reqB := httptest.NewRequest("GET", "/", nil)
	reqB.Header.Set("X-API-Key", "B")
	wB := httptest.NewRecorder()
	handler.ServeHTTP(wB, reqB)
	if wB.Code != http.StatusOK {
		t.Error("client B failed")
	}

	// Client A again (blocked).
	wA2 := httptest.NewRecorder()
	handler.ServeHTTP(wA2, reqA)
	if wA2.Code != http.StatusTooManyRequests {
		t.Error("client A should be blocked")
	}
}
