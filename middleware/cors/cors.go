// Package cors provides CORS middleware for the revelt framework.
package cors

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/abiiranathan/revelt"
)

// CORSOptions is the configuration for the CORS middleware.
type CORSOptions struct {
	AllowedOrigins   []string // Origins that are allowed in the request; default is all origins.
	AllowedMethods   []string // Methods that are allowed in the request.
	AllowedHeaders   []string // Headers that are allowed in the request.
	ExposedHeaders   []string // Headers that are exposed to the client.
	AllowCredentials bool     // Allow credentials like cookies, authorization headers.
	MaxAge           int      // Max age in seconds to cache preflight requests.
	Allowwebsockets  bool     // Allow websockets.
}

// New creates a CORS middleware with default options. If opts is provided,
// it is used instead of defaults; all CORSOptions fields must be set
// explicitly since there is no merging with defaults in that case.
// If the request's Origin is not allowed, a 403 status code is sent.
func New(opts ...CORSOptions) func(revelt.HandlerFunc) revelt.HandlerFunc {
	options := CORSOptions{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{http.MethodGet, http.MethodOptions, http.MethodPost, http.MethodPut, http.MethodDelete},
		AllowedHeaders:   []string{"*"},
		AllowCredentials: false,
		MaxAge:           5,
		Allowwebsockets:  false,
	}

	if len(opts) > 0 {
		options = opts[0]
	}

	return func(next revelt.HandlerFunc) revelt.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) error {
			origin := r.Header.Get("Origin")

			if len(options.AllowedOrigins) > 0 {
				allowed := false
				for _, v := range options.AllowedOrigins {
					if v == origin || v == "*" {
						allowed = true
						break
					}
				}

				if !allowed {
					w.WriteHeader(http.StatusForbidden)
					return nil
				}
			}

			w.Header().Set("Access-Control-Allow-Origin", origin)

			if len(options.AllowedMethods) > 0 {
				w.Header().Set("Access-Control-Allow-Methods", strings.Join(options.AllowedMethods, ", "))
			}
			if len(options.AllowedHeaders) > 0 {
				w.Header().Set("Access-Control-Allow-Headers", strings.Join(options.AllowedHeaders, ", "))
			}
			if len(options.ExposedHeaders) > 0 {
				w.Header().Set("Access-Control-Expose-Headers", strings.Join(options.ExposedHeaders, ", "))
			}
			if options.AllowCredentials {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			if options.MaxAge > 0 {
				w.Header().Set("Access-Control-Max-Age", fmt.Sprintf("%d", options.MaxAge))
			}
			if options.Allowwebsockets {
				w.Header().Set("Access-Control-Allow-Websocket", "true")
			}

			// Handle preflight requests and return early.
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return nil
			}

			return next(w, r)
		}
	}
}
