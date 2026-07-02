// Package etag provides ETag middleware for the revelt framework.
package etag

import (
	"bufio"
	"bytes"
	"crypto/sha1" // for content hashing; ETags are not a security boundary
	"fmt"
	"hash"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/abiiranathan/revelt"
)

// writerPool reuses etagResponseWriter instances to avoid per-request heap allocation.
var writerPool = sync.Pool{
	New: func() any {
		ew := &etagResponseWriter{}
		ew.hash = sha1.New()
		return ew
	},
}

// etagResponseWriter intercepts writes to buffer the body and compute a hash
// simultaneously, deferring the actual write until the ETag is known.
type etagResponseWriter struct {
	http.ResponseWriter
	buf         bytes.Buffer
	hash        hash.Hash
	status      int
	wroteHeader bool
}

// reset re-initializes a pooled etagResponseWriter for a new request.
func (e *etagResponseWriter) reset(w http.ResponseWriter) {
	e.ResponseWriter = w
	e.buf.Reset()
	e.hash.Reset()
	e.status = http.StatusOK
	e.wroteHeader = false
}

// WriteHeader captures the status code. For 200 responses we defer the
// actual WriteHeader call until we know the ETag; for all others we pass
// through immediately.
func (e *etagResponseWriter) WriteHeader(code int) {
	if e.wroteHeader {
		return
	}
	e.wroteHeader = true
	e.status = code
	if code != http.StatusOK {
		e.ResponseWriter.WriteHeader(code)
	}
}

// Write fans out to both the hash and the buffer for 200 responses.
// Non-200 writes bypass buffering entirely.
func (e *etagResponseWriter) Write(p []byte) (int, error) {
	if !e.wroteHeader {
		e.wroteHeader = true
		e.status = http.StatusOK
	}
	if e.status != http.StatusOK {
		n, err := e.ResponseWriter.Write(p)
		if err != nil {
			return n, fmt.Errorf("etag: writing passthrough body: %w", err)
		}
		return n, nil
	}
	// Write to hash and buf in two calls — avoids io.MultiWriter allocation
	// and keeps the two writes on the same cache lines.
	n, err := e.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("etag: buffering response body: %w", err)
	}
	_, _ = e.hash.Write(p) // sha1.Write never returns an error
	return n, nil
}

// Unwrap lets http.ResponseController reach the underlying writer (Go 1.20+).
func (e *etagResponseWriter) Unwrap() http.ResponseWriter {
	return e.ResponseWriter
}

// Flush forwards to the underlying writer if it supports flushing.
// Note: flushing mid-response means an ETag can no longer be computed for
// the full body, so callers should not mix streaming with ETag middleware.
func (e *etagResponseWriter) Flush() {
	if f, ok := e.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack supports WebSocket upgrades on the underlying connection.
func (e *etagResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := e.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// SkipFunc reports whether ETag processing should be skipped for a request.
type SkipFunc func(r *http.Request) bool

// New returns middleware that computes and validates ETags for cacheable
// GET/HEAD responses. It buffers the response body only for 200 OK
// responses; all other status codes are passed through without buffering or
// hashing.
//
// Optional SkipFunc predicates short-circuit ETag processing when any
// returns true.
func New(skip ...SkipFunc) func(revelt.HandlerFunc) revelt.HandlerFunc {
	return func(next revelt.HandlerFunc) revelt.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) error {
			// Fast-path: skip non-cacheable requests without touching the pool.
			if !isCacheable(r, skip) {
				return next(w, r)
			}

			ew := writerPool.Get().(*etagResponseWriter)
			ew.reset(w)

			err := next(ew, r)

			// Return the writer to the pool before any early returns so it
			// is available to other goroutines as soon as possible.
			status := ew.status
			etagValue := fmt.Sprintf(`"%x"`, ew.hash.Sum(nil))
			body := ew.buf.Bytes()

			// Guard against oversized buffers persisting in the pool.
			if ew.buf.Cap() > 512<<10 { // 512 KB
				// Discard the oversized writer; the pool will allocate a
				// fresh one via New() when next needed.
			} else {
				writerPool.Put(ew)
			}

			if err != nil {
				return err
			}

			// Non-200 responses were already written through; nothing left to do.
			if status != http.StatusOK {
				return nil
			}

			// Validate conditional request headers before committing the response.
			if r.Header.Get("If-None-Match") == etagValue {
				w.WriteHeader(http.StatusNotModified)
				return nil
			}

			ifMatch := r.Header.Get("If-Match")
			if ifMatch != "" && ifMatch != etagValue {
				w.WriteHeader(http.StatusPreconditionFailed)
				return nil
			}

			// Commit: write ETag, status, then body.
			w.Header().Set("ETag", etagValue)
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write(body); err != nil {
				return fmt.Errorf("etag: writing committed body: %w", err)
			}
			return nil
		}
	}
}

// isCacheable returns true when ETag processing should run for this request.
func isCacheable(r *http.Request, skip []SkipFunc) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		return false
	}
	for _, s := range skip {
		if s(r) {
			return false
		}
	}
	return true
}
