// Package recovery provides panic recovery middleware for the revelt framework.
package recovery

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"runtime/debug"

	"github.com/abiiranathan/revelt"
)

// Config holds configuration for the recovery middleware.
type Config struct {
	// StackTrace, if true, includes a full stack trace in logged output.
	StackTrace bool

	// ErrorHandler, if non-nil, handles the recovered error completely,
	// bypassing the default response-writing logic below.
	ErrorHandler func(w http.ResponseWriter, r *http.Request, err error)

	// Logger receives recovery diagnostics. Defaults to the standard log package.
	Logger Logger

	// ExposeErrors, if true, sends the raw error message to the client.
	// Disable in production to avoid leaking internal details.
	ExposeErrors bool

	// ProductionMessage is the generic error message sent to clients when
	// ExposeErrors is false.
	ProductionMessage string
}

// Logger is the minimal logging interface required by the recovery middleware.
type Logger interface {
	// Printf logs a formatted message.
	Printf(format string, v ...any)
}

// defaultLogger adapts the standard log package to the Logger interface.
type defaultLogger struct{}

// Printf logs recovery output using the standard library logger.
func (defaultLogger) Printf(format string, v ...any) {
	log.Printf(format, v...)
}

// Option configures a Config via functional options.
type Option func(*Config)

// WithStackTrace configures whether to log stack traces on recovery.
func WithStackTrace(enabled bool) Option {
	return func(c *Config) { c.StackTrace = enabled }
}

// WithErrorHandler sets a custom error handler, overriding the default
// status/body response logic entirely.
func WithErrorHandler(handler func(w http.ResponseWriter, r *http.Request, err error)) Option {
	return func(c *Config) { c.ErrorHandler = handler }
}

// WithLogger sets a custom logger for recovery diagnostics.
func WithLogger(logger Logger) Option {
	return func(c *Config) { c.Logger = logger }
}

// WithExposeErrors configures whether error details are exposed to clients.
func WithExposeErrors(expose bool) Option {
	return func(c *Config) { c.ExposeErrors = expose }
}

// WithProductionMessage sets the generic error message used when
// ExposeErrors is false.
func WithProductionMessage(message string) Option {
	return func(c *Config) { c.ProductionMessage = message }
}

// New creates panic-recovery middleware with the given options. Any panic
// raised by a downstream handler is recovered, logged, and converted into a
// 500 Internal Server Error response (or handled by a custom ErrorHandler).
func New(opts ...Option) func(revelt.HandlerFunc) revelt.HandlerFunc {
	config := Config{
		StackTrace:        false,
		Logger:            defaultLogger{},
		ExposeErrors:      false,
		ProductionMessage: "Internal Server Error",
	}
	for _, opt := range opts {
		opt(&config)
	}

	return func(next revelt.HandlerFunc) revelt.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) (retErr error) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}

				err := convertPanicToError(rec)
				logError(config, err)

				if config.ErrorHandler != nil {
					config.ErrorHandler(w, r, err)
					return
				}
				sendErrorResponse(w, r, err, config)
			}()

			return next(w, r)
		}
	}
}

// convertPanicToError safely converts a recovered panic value to an error.
func convertPanicToError(r any) error {
	switch v := r.(type) {
	case error:
		return v
	case string:
		return errors.New(v)
	case fmt.Stringer:
		return errors.New(v.String())
	default:
		return fmt.Errorf("panic: %+v", v)
	}
}

// logError logs the recovered error, optionally including a stack trace.
func logError(config Config, err error) {
	if config.StackTrace {
		config.Logger.Printf("Panic recovered: %v\nStack trace:\n%s", err, string(debug.Stack()))
	} else {
		config.Logger.Printf("Panic recovered: %v\n", err)
	}
}

// sendErrorResponse writes a default error response, choosing JSON or plain
// text based on the request's declared Content-Type and Accept headers.
func sendErrorResponse(w http.ResponseWriter, r *http.Request, err error, config Config) {
	message := config.ProductionMessage
	if config.ExposeErrors {
		message = err.Error()
	}

	contentType := r.Header.Get("Content-Type")
	accept := r.Header.Get("Accept")
	wantsJSON := contentType == "application/json" || accept == "application/json"

	w.WriteHeader(http.StatusInternalServerError)

	if wantsJSON {
		w.Header().Set("Content-Type", "application/json")
		data, marshalErr := json.Marshal(map[string]any{"error": message})
		if marshalErr != nil {
			// Marshalling a two-field string map cannot realistically fail,
			// but fall back to plain text rather than silently dropping the body.
			_, _ = w.Write([]byte(message))
			return
		}
		_, _ = w.Write(data)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(message))
}
