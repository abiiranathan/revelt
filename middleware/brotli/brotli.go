// Package brotli provides Brotli compression middleware for the revelt framework.
package brotli

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/abiiranathan/revelt"
	"github.com/andybalholm/brotli"
	"github.com/andybalholm/brotli/matchfinder"
)

// brotliWriter wraps http.ResponseWriter, transparently compressing the
// response body with Brotli and adjusting headers accordingly. It defers
// writing the status line until the first Write or explicit WriteHeader
// call, matching standard net/http semantics.
type brotliWriter struct {
	http.ResponseWriter
	bw            *matchfinder.Writer
	status        int
	headerWritten bool
}

// WriteHeader writes the response status code and compression headers.
// Subsequent calls after the first are no-ops, matching the contract of
// http.ResponseWriter.
func (b *brotliWriter) WriteHeader(code int) {
	if b.headerWritten {
		return
	}
	b.status = code
	if code != http.StatusNoContent && code != http.StatusNotModified {
		b.ResponseWriter.Header().Set("Content-Encoding", "br")
		b.ResponseWriter.Header().Del("Content-Length")
	}
	b.ResponseWriter.WriteHeader(code)
	b.headerWritten = true
}

// Write compresses p and writes it to the underlying response, implicitly
// committing a 200 OK status if WriteHeader has not yet been called.
func (b *brotliWriter) Write(p []byte) (int, error) {
	if !b.headerWritten {
		code := b.status
		if code == 0 {
			code = http.StatusOK
		}
		b.WriteHeader(code)
	}
	n, err := b.bw.Write(p)
	if err != nil {
		return n, fmt.Errorf("brotli: writing compressed body: %w", err)
	}
	return n, nil
}

// Flush flushes any buffered response data to the underlying connection.
func (b *brotliWriter) Flush() {
	if flusher, ok := b.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Hijack implements http.Hijacker when supported by the underlying writer,
// so WebSocket upgrades pass through unaffected.
func (b *brotliWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := b.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("brotli: http.Hijacker interface is not supported")
}

// Brotli returns middleware that compresses responses with Brotli when the
// client's Accept-Encoding header advertises support for it. Requests whose
// path has any of skipPaths as a prefix bypass compression entirely.
func Brotli(skipPaths ...string) func(revelt.HandlerFunc) revelt.HandlerFunc {
	return func(next revelt.HandlerFunc) revelt.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) error {
			for _, path := range skipPaths {
				if strings.HasPrefix(r.URL.Path, path) {
					return next(w, r)
				}
			}

			if !strings.Contains(r.Header.Get("Accept-Encoding"), "br") {
				return next(w, r)
			}

			bw := brotli.NewWriterV2(w, 7)
			wrapped := &brotliWriter{ResponseWriter: w, bw: bw}

			defer func() {
				// A 204/304 response has no body; closing the Brotli writer
				// in that case would emit a spurious trailer with nothing to
				// flush against, so skip it.
				if wrapped.headerWritten &&
					(wrapped.status == http.StatusNoContent ||
						wrapped.status == http.StatusNotModified) {
					return
				}
				_ = bw.Close()
			}()

			return next(wrapped, r)
		}
	}
}
