// Package requestid provides request ID middleware for the revelt framework.
package requestid

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/abiiranathan/revelt"
)

// HeaderKey is the default header key for the request ID.
const HeaderKey = "X-Request-ID"

// Config defines the configuration for the RequestID middleware.
type Config struct {
	// Generator produces a new ID. Defaults to a random 32-character hex string.
	Generator func() string

	// Header is the header key to read/set. Defaults to HeaderKey.
	Header string
}

// New creates request-ID middleware with default configuration.
func New() func(revelt.HandlerFunc) revelt.HandlerFunc {
	return WithConfig(Config{})
}

// WithConfig creates request-ID middleware with the given configuration. If
// the incoming request already carries the configured header, that value is
// reused; otherwise a new ID is generated. The ID is set on both the
// request (for downstream handler visibility) and the response.
func WithConfig(config Config) func(revelt.HandlerFunc) revelt.HandlerFunc {
	if config.Generator == nil {
		config.Generator = randomID
	}
	if config.Header == "" {
		config.Header = HeaderKey
	}

	return func(next revelt.HandlerFunc) revelt.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) error {
			rid := r.Header.Get(config.Header)
			if rid == "" {
				rid = config.Generator()
				r.Header.Set(config.Header, rid)
			}

			w.Header().Set(config.Header, rid)
			return next(w, r)
		}
	}
}

// randomID generates a random 32-character hex string.
func randomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read on a properly functioning system does not fail;
		// if it somehow does, degrade to an all-zero ID rather than panicking
		// mid-request. The all-zero pattern makes such a failure conspicuous
		// in logs.
		return hex.EncodeToString(b)
	}
	return hex.EncodeToString(b)
}
