// Package auth provides authentication middleware for the revelt framework:
// HTTP Basic auth, cookie-based sessions, and JWT bearer tokens.
package auth

import (
	"crypto/subtle"
	"fmt"
	"net/http"

	"github.com/abiiranathan/revelt"
)

// BasicAuth returns middleware that protects routes with HTTP Basic authentication.
// If the credentials are invalid, it responds with status 401 and a WWW-Authenticate
// header naming realm; if realm is omitted, "Restricted" is used.
//
// The returned middleware is safe for concurrent use by multiple goroutines.
func BasicAuth(username, password string, realm ...string) func(revelt.HandlerFunc) revelt.HandlerFunc {
	defaultRealm := "Restricted"
	if len(realm) > 0 {
		defaultRealm = realm[0]
	}

	return func(next revelt.HandlerFunc) revelt.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) error {
			user, pass, ok := r.BasicAuth()

			// Use constant-time comparison to avoid leaking credential length
			// or content through timing side channels.
			if !ok || subtle.ConstantTimeCompare([]byte(user), []byte(username)) != 1 ||
				subtle.ConstantTimeCompare([]byte(pass), []byte(password)) != 1 {

				w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Basic realm="%s"`, defaultRealm))
				w.WriteHeader(http.StatusUnauthorized)
				return nil
			}
			return next(w, r)
		}
	}
}
