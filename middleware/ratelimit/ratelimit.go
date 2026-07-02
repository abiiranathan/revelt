package ratelimit

import (
	"net"
	"net/http"
	"time"

	"github.com/abiiranathan/revelt"
)

// Config defines the configuration for the RateLimit middleware.
type Config struct {
	// Rate is the number of requests allowed per second.
	Rate float64

	// Capacity is the maximum burst size.
	Capacity float64

	// KeyFunc generates a rate-limiting key for the request (e.g. client
	// IP). Defaults to the request's remote IP if nil.
	KeyFunc func(r *http.Request) string

	// Expiration is the duration after which an idle limiter bucket is
	// removed from memory. Default: 1 minute.
	Expiration time.Duration

	// ErrorHandler is called when the limit is exceeded. Default: returns
	// 429 Too Many Requests.
	ErrorHandler func(w http.ResponseWriter, r *http.Request) error
}

// defaultKeyFunc extracts the client IP from r.RemoteAddr for use as the
// default rate-limiting key.
func defaultKeyFunc(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// New creates rate-limiting middleware with the given configuration. It
// panics if Rate or Capacity is non-positive, since these indicate a
// programming error rather than a runtime condition.
func New(config Config) func(revelt.HandlerFunc) revelt.HandlerFunc {
	if config.Rate <= 0 {
		panic("ratelimit: Rate must be positive")
	}
	if config.Capacity <= 0 {
		panic("ratelimit: Capacity must be positive")
	}
	if config.KeyFunc == nil {
		config.KeyFunc = defaultKeyFunc
	}
	if config.Expiration == 0 {
		config.Expiration = time.Minute
	}
	if config.ErrorHandler == nil {
		config.ErrorHandler = func(w http.ResponseWriter, _ *http.Request) error {
			w.WriteHeader(http.StatusTooManyRequests)
			_, err := w.Write([]byte("Too Many Requests"))
			return err
		}
	}

	manager := NewManager(config.Rate, config.Capacity, config.Expiration)

	return func(next revelt.HandlerFunc) revelt.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) error {
			key := config.KeyFunc(r)
			if !manager.Allow(key) {
				return config.ErrorHandler(w, r)
			}
			return next(w, r)
		}
	}
}
