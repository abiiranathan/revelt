// Package gzip provides gzip compression middleware for the revelt framework.
package gzip

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/abiiranathan/revelt"
)

// gzipWriter wraps http.ResponseWriter, transparently compressing the
// response body with gzip and adjusting headers accordingly.
type gzipWriter struct {
	http.ResponseWriter
	gw            *gzip.Writer
	status        int
	headerWritten bool
}

// WriteHeader writes the response status code and gzip headers. Subsequent
// calls after the first are no-ops, matching http.ResponseWriter's contract.
func (g *gzipWriter) WriteHeader(code int) {
	if g.headerWritten {
		return
	}
	g.status = code
	if code != http.StatusNoContent && code != http.StatusNotModified {
		g.ResponseWriter.Header().Set("Content-Encoding", "gzip")
		g.ResponseWriter.Header().Del("Content-Length")
	}
	g.headerWritten = true
	g.ResponseWriter.WriteHeader(code)
}

// Write compresses p and writes it to the underlying response.
func (g *gzipWriter) Write(p []byte) (int, error) {
	if !g.headerWritten {
		code := g.status
		if code == 0 {
			code = http.StatusOK
		}
		g.WriteHeader(code)
	}
	n, err := g.gw.Write(p)
	if err != nil {
		return n, fmt.Errorf("gzip: writing compressed body: %w", err)
	}
	return n, nil
}

// Flush flushes any buffered response data.
func (g *gzipWriter) Flush() {
	if g.gw != nil {
		_ = g.gw.Flush()
	}
	if flusher, ok := g.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Hijack implements http.Hijacker when supported by the underlying writer.
func (g *gzipWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := g.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("gzip: http.Hijacker interface is not supported")
}

// Gzip returns middleware that compresses responses with gzip using the
// default compression level, when the client's Accept-Encoding header
// advertises support for it. Requests whose path has any of skipPaths as a
// prefix bypass compression entirely.
func Gzip(skipPaths ...string) func(revelt.HandlerFunc) revelt.HandlerFunc {
	return GzipLevel(gzip.DefaultCompression, skipPaths...)
}

// GzipLevel creates gzip middleware with a specific compression level (one
// of the gzip.* level constants). It panics if level is out of range, since
// this indicates a programming error rather than a runtime condition.
func GzipLevel(level int, skipPaths ...string) func(revelt.HandlerFunc) revelt.HandlerFunc {
	if level < gzip.HuffmanOnly || level > gzip.BestCompression {
		panic(fmt.Errorf("gzip: invalid compression level: %d", level))
	}

	return func(next revelt.HandlerFunc) revelt.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) error {
			for _, path := range skipPaths {
				if strings.HasPrefix(r.URL.Path, path) {
					return next(w, r)
				}
			}

			// If some handler/middleware has already set a Content-Encoding, skip compression.
			if w.Header().Get("Content-Encoding") != "" {
				return next(w, r)
			}

			// If the client doesn't support gzip, skip compression.
			if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				return next(w, r)
			}

			gw, err := gzip.NewWriterLevel(w, level)
			if err != nil {
				// Level was already validated above, but handle defensively
				// in case of future changes; fall back to uncompressed.
				return next(w, r)
			}
			wrapped := &gzipWriter{ResponseWriter: w, gw: gw}

			defer func() {
				// A 204/304 response has no body; closing the gzip writer in
				// that case would emit a spurious trailer with nothing to
				// flush against, so skip it.
				if wrapped.headerWritten && (wrapped.status == http.StatusNoContent || wrapped.status == http.StatusNotModified) {
					return
				}
				_ = gw.Close()
			}()

			return next(wrapped, r)
		}
	}
}
