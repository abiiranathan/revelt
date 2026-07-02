// Package logger provides request logging middleware for the revelt framework.
package logger

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/abiiranathan/revelt"
)

// LogFormat is the format of the log output, compatible with the slog package.
type LogFormat int

// LogFlags controls which request attributes are added to log output.
type LogFlags int8

// Supported log output formats.
const (
	TextFormat LogFormat = iota + 1 // Human-readable key=value text format. Default.
	JSONFormat                      // Structured JSON format.
)

// Individual log field toggles, combinable via bitwise OR.
const (
	LogIP        LogFlags = 1 << iota // Include the client IP address.
	LogLatency                        // Include request handling latency.
	LogUserAgent                      // Include the User-Agent header.
)

// StdLogFlags is the default set of log fields included by the middleware.
const StdLogFlags LogFlags = LogLatency | LogIP

// Config configures the request logging middleware.
type Config struct {
	// Output is the destination for the log output. If nil, os.Stderr is used.
	Output io.Writer

	// Format is the format of the log output. Default is TextFormat.
	Format LogFormat

	// Flags controls which fields are logged. Default is StdLogFlags.
	Flags LogFlags

	// Skip is a slice of exact request paths that should not be logged.
	Skip []string

	// SkipIf, if non-nil, is called per-request; if it returns true, the
	// request is not logged.
	SkipIf func(r *http.Request) bool

	// Options configures the underlying slog.Handler.
	Options *slog.HandlerOptions

	// Callback, if non-nil, can append or modify the key/value argument list
	// passed to the logger (e.g. to add a request ID or user ID). It must
	// return an even number of arguments.
	Callback func(w http.ResponseWriter, r *http.Request, args ...any) []any
}

// DefaultConfig is the default logger configuration used by New when passed
// nil. It writes logs to os.Stderr with TextFormat and StdLogFlags, at
// slog.LevelInfo.
var DefaultConfig = &Config{
	Output: os.Stderr,
	Format: TextFormat,
	Flags:  StdLogFlags,
	Options: &slog.HandlerOptions{
		Level:     slog.LevelInfo,
		AddSource: false,
	},
}

// statusCapturingWriter wraps http.ResponseWriter solely to observe the
// status code written by downstream handlers, without altering response
// content in any way.
type statusCapturingWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

// WriteHeader records the status code before delegating to the underlying writer.
func (s *statusCapturingWriter) WriteHeader(code int) {
	if s.wroteHeader {
		return
	}
	s.wroteHeader = true
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Write implicitly records a 200 OK status if WriteHeader has not yet been called.
func (s *statusCapturingWriter) Write(p []byte) (int, error) {
	if !s.wroteHeader {
		s.WriteHeader(http.StatusOK)
	}
	return s.ResponseWriter.Write(p) //nolint:wrapcheck // passthrough writer
}

// Unwrap lets http.ResponseController reach the underlying writer (Go 1.20+).
func (s *statusCapturingWriter) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}

// clientIP extracts the request's client IP, preferring the X-Real-Ip
// header (as set by a trusted reverse proxy) and falling back to
// r.RemoteAddr.
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-Ip"); ip != "" {
		return ip
	}
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		// X-Forwarded-For may contain a comma-separated chain; the first
		// entry is the original client.
		if before, _, ok := strings.Cut(ip, ","); ok {
			return strings.TrimSpace(before)
		}
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// New returns a new logger middleware with the provided configuration. A
// nil config falls back to DefaultConfig. The logger must be placed before
// any middleware that wraps the response writer in a way that obscures the
// final status code (it observes status via its own wrapper, so ordering
// relative to compression middleware is safe either way).
func New(config *Config) func(revelt.HandlerFunc) revelt.HandlerFunc {
	if config == nil {
		config = DefaultConfig
	}
	if config.Output == nil {
		config.Output = os.Stderr
	}
	if config.Format == 0 {
		config.Format = TextFormat
	}
	if config.Options == nil {
		config.Options = &slog.HandlerOptions{
			Level:     slog.LevelInfo,
			AddSource: false,
		}
	}

	return func(next revelt.HandlerFunc) revelt.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) error {
			if slices.Contains(config.Skip, r.URL.Path) {
				return next(w, r)
			}
			if config.SkipIf != nil && config.SkipIf(r) {
				return next(w, r)
			}

			sw := &statusCapturingWriter{ResponseWriter: w}
			start := time.Now()
			err := next(sw, r)
			latency := time.Since(start)

			var logHandler slog.Handler
			switch config.Format {
			case JSONFormat:
				logHandler = slog.NewJSONHandler(config.Output, config.Options)
			default:
				logHandler = slog.NewTextHandler(config.Output, config.Options)
			}
			slogger := slog.New(logHandler)

			status := sw.status
			if status == 0 {
				status = http.StatusOK
			}

			args := make([]any, 0, 10)
			args = append(args, "status", status)
			if config.Flags&LogLatency != 0 {
				args = append(args, "latency", latency.String())
			}
			args = append(args, "method", r.Method, "path", r.URL.Path)
			if config.Flags&LogIP != 0 {
				args = append(args, "ip", clientIP(r))
			}
			if config.Flags&LogUserAgent != 0 {
				args = append(args, "user_agent", r.UserAgent())
			}

			if config.Callback != nil {
				newArgs := config.Callback(sw, r, args...)
				if len(newArgs)%2 != 0 {
					return fmt.Errorf("logger: Callback must return an even number of arguments")
				}
				args = newArgs
			}

			slogger.Info("", args...)
			return err
		}
	}
}
