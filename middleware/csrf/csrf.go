// Package csrf provides CSRF protection middleware for the revelt framework.
package csrf

import (
	"context" // for context.WithValue
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"

	"github.com/abiiranathan/revelt"
	"github.com/gorilla/sessions"
)

const (
	formKeyName = "csrf_token" // Form field name carrying the CSRF token.
	cookieName  = "csrf_token" // Cookie name carrying the CSRF token.
)

// tokenCtxKey is the context key under which the current request's CSRF
// token is stashed for retrieval via Token.
type tokenCtxKey struct{}

// Sentinel errors describing CSRF validation failures.
var (
	ErrMissingToken = errors.New("csrf: missing CSRF token")
	ErrInvalidToken = errors.New("csrf: invalid CSRF token")
)

// CreateToken generates a random, base64-encoded CSRF token.
func CreateToken() (string, error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("csrf: generating random token: %w", err)
	}
	return base64.StdEncoding.EncodeToString(tokenBytes), nil
}

// isSafeMethod reports whether method is one of the HTTP methods considered
// safe (no side effects) per RFC 7231 §4.2.1, and therefore exempt from
// CSRF validation.
func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

// New returns middleware that sets and verifies CSRF tokens using cookies
// and forms. Render the token in HTML forms via Token(r) and a hidden input
// named "csrf_token". If secureCookie is true, the token cookie is
// transmitted only over HTTPS.
//
// store is accepted for API-compatibility with session-backed CSRF setups
// but is not currently used internally; the token round-trips via a
// dedicated HTTP-only cookie rather than the session store.
func New(store sessions.Store, secureCookie bool) func(revelt.HandlerFunc) revelt.HandlerFunc {
	_ = store // reserved for future session-backed token storage

	return func(next revelt.HandlerFunc) revelt.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) error {
			token, err := getOrCreateToken(w, r, secureCookie)
			if err != nil {
				return fmt.Errorf("csrf: unable to create CSRF token: %w", err)
			}

			ctx := context.WithValue(r.Context(), tokenCtxKey{}, token)
			r = r.WithContext(ctx)

			// Safe methods (GET, HEAD, OPTIONS, TRACE) never mutate state,
			// so they are exempt from CSRF validation.
			if isSafeMethod(r.Method) {
				return next(w, r)
			}

			if !validateCSRFToken(r) {
				http.Error(w, "Forbidden: CSRF token validation failed", http.StatusForbidden)
				return nil
			}

			return next(w, r)
		}
	}
}

// Token returns the CSRF token associated with the current request. It MUST
// be called after the CSRF middleware has run on the request (e.g. from
// within a downstream handler rendering a form).
func Token(r *http.Request) string {
	token, _ := r.Context().Value(tokenCtxKey{}).(string)
	return token
}

// getOrCreateToken retrieves the token from the request's cookie, or
// generates and sets a new one if absent.
func getOrCreateToken(w http.ResponseWriter, r *http.Request, secureCookie bool) (string, error) {
	if cookie, err := r.Cookie(cookieName); err == nil {
		return cookie.Value, nil
	}

	token, err := CreateToken()
	if err != nil {
		return "", err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",          // Make cookie available across the site.
		HttpOnly: true,         // Prevent access via JavaScript.
		Secure:   secureCookie, // Use HTTPS only in prod (false for local testing).
		SameSite: http.SameSiteLaxMode,
	})
	return token, nil
}

// validateCSRFToken checks the token from the form or request header
// against the cookie, using a constant-time comparison to avoid timing
// side channels.
func validateCSRFToken(r *http.Request) bool {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}

	token := r.FormValue(formKeyName)
	if token == "" {
		token = r.Header.Get("X-CSRF-Token")
		if token == "" {
			return false
		}
	}
	return subtleCompare(token, cookie.Value)
}

// subtleCompare performs a constant-time comparison of two strings to avoid
// timing attacks that could reveal the valid token byte-by-byte.
func subtleCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
