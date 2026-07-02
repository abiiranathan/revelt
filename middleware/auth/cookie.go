package auth

import (
	"context" // for context.WithValue
	"encoding/gob"
	"errors"
	"fmt" // for fmt.Errorf
	"net/http"
	"time"

	"github.com/abiiranathan/revelt"
	"github.com/gorilla/sessions"
)

// ctxKey is an unexported type for context keys defined in this package,
// preventing collisions with keys defined in other packages.
type ctxKey string

// Context keys used to stash auth-related values on the request context,
// since revelt.HandlerFunc has no per-request state container of its own.
const (
	authSkippedKey ctxKey = "cookie_auth_skipped"
	stateKey       string = "rex_auth_state"
	authKey        string = "rex_authenticated"
	lastAccessKey  string = "last_access"
)

// ErrNotInitialized is returned when a CookieAuth instance is nil or missing its store.
var ErrNotInitialized = errors.New("auth: cookie auth is not initialized")

// CookieAuth encapsulates session cookie authentication state and behavior.
// Safe for concurrent use by multiple goroutines once constructed.
type CookieAuth struct {
	store       *sessions.CookieStore
	sessionName string
	config      CookieConfig
	maxAge      time.Duration
	refreshAge  time.Duration
}

// CookieConfig defines the behavior of the cookie authentication middleware.
type CookieConfig struct {
	// Options controls cookie behavior. Default: HttpOnly=true, SameSite=Strict
	// (always enforced), MaxAge=24h, Path="/", Secure=false.
	Options *sessions.Options

	// SkipAuth, if non-nil, is called per-request; if it returns true,
	// authentication is skipped for that request.
	SkipAuth func(r *http.Request) bool

	// ErrorHandler is invoked when authentication fails. Must write a
	// complete HTTP response.
	ErrorHandler func(w http.ResponseWriter, r *http.Request) error
}

// DefaultErrorHandler writes HTTP 401 for unauthenticated requests.
func DefaultErrorHandler(w http.ResponseWriter, _ *http.Request) error {
	w.WriteHeader(http.StatusUnauthorized)
	return nil
}

// normalizeCookieConfig fills in defaults and enforces the security
// invariants (HttpOnly, SameSiteStrictMode) regardless of caller input.
func normalizeCookieConfig(config CookieConfig) CookieConfig {
	if config.ErrorHandler == nil {
		config.ErrorHandler = DefaultErrorHandler
	}

	if config.Options == nil {
		config.Options = &sessions.Options{
			Path:     "/",
			MaxAge:   int((24 * time.Hour).Seconds()),
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		}
	} else {
		config.Options = &sessions.Options{
			Path:     config.Options.Path,
			Domain:   config.Options.Domain,
			MaxAge:   config.Options.MaxAge,
			Secure:   config.Options.Secure,
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		}

		if config.Options.MaxAge <= 0 {
			config.Options.MaxAge = int((24 * time.Hour).Seconds())
		}
		if config.Options.Path == "" {
			config.Options.Path = "/"
		}
	}

	return config
}

// NewCookieAuth creates a cookie authentication instance with its own store
// and session name. userType is registered with encoding/gob so it can be
// stored in the session; it must be a non-nil zero value of the concrete
// type that will be passed to SetState.
func NewCookieAuth(sessionName string, keyPairs [][]byte, userType any, config CookieConfig) (*CookieAuth, error) {
	if sessionName == "" {
		return nil, errors.New("auth: sessionName is required")
	}
	if len(keyPairs) < 1 {
		return nil, errors.New("auth: you must pass at least one keyPair")
	}
	if userType == nil {
		return nil, errors.New("auth: userType must not be nil")
	}

	gob.Register(userType)
	gob.Register(time.Time{})

	config = normalizeCookieConfig(config)
	store := sessions.NewCookieStore(keyPairs...)
	store.Options = config.Options

	maxAge := time.Duration(config.Options.MaxAge) * time.Second
	return &CookieAuth{
		store:       store,
		sessionName: sessionName,
		config:      config,
		maxAge:      maxAge,
		refreshAge:  maxAge / 2,
	}, nil
}

// unauthenticated dispatches to SkipAuth (marking the context accordingly)
// or falls through to the configured ErrorHandler.
func (a *CookieAuth) unauthenticated(w http.ResponseWriter, r *http.Request, next revelt.HandlerFunc) error {
	if a.config.SkipAuth != nil && a.config.SkipAuth(r) {
		ctx := context.WithValue(r.Context(), authSkippedKey, true)
		return next(w, r.WithContext(ctx))
	}
	return a.config.ErrorHandler(w, r)
}

// expire overwrites the session cookie with one that is already expired,
// forcing the client to discard it.
func (a *CookieAuth) expire(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     a.sessionName,
		Path:     a.config.Options.Path,
		Domain:   a.config.Options.Domain,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
		HttpOnly: a.config.Options.HttpOnly,
		Secure:   a.config.Options.Secure,
		SameSite: a.config.Options.SameSite,
	})
}

// Middleware returns the cookie authentication middleware for this instance.
// It implements a sliding-window session: the session's last-access
// timestamp is refreshed once more than half of maxAge has elapsed since it
// was last saved, extending the session's lifetime without requiring the
// client to re-authenticate.
func (a *CookieAuth) Middleware() func(revelt.HandlerFunc) revelt.HandlerFunc {
	return func(next revelt.HandlerFunc) revelt.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) error {
			session, err := a.store.Get(r, a.sessionName)
			if err != nil {
				a.expire(w)
				return a.unauthenticated(w, r, next)
			}

			if session.Values[authKey] != true {
				return a.unauthenticated(w, r, next)
			}

			now := time.Now()
			lastAccess, ok := session.Values[lastAccessKey].(time.Time)
			if !ok {
				return a.unauthenticated(w, r, next)
			}

			sessionAge := now.Sub(lastAccess)
			if sessionAge > a.maxAge {
				return a.unauthenticated(w, r, next)
			}

			if sessionAge > a.refreshAge {
				session.Values[lastAccessKey] = now
				session.Options = a.config.Options
				if err := session.Save(r, w); err != nil {
					return fmt.Errorf("auth: saving refreshed session: %w", err)
				}
			}

			ctx := context.WithValue(r.Context(), stateCtxKey, session.Values[stateKey])
			return next(w, r.WithContext(ctx))
		}
	}
}

// stateCtxKey is the context key under which the authenticated state value
// is stashed for retrieval via Value.
const stateCtxKey ctxKey = "rex_auth_state_ctx"

// SetState stores authentication state for this instance and persists the
// session cookie to the response.
func (a *CookieAuth) SetState(w http.ResponseWriter, r *http.Request, state any) error {
	if a == nil || a.store == nil {
		return ErrNotInitialized
	}

	session, err := a.store.Get(r, a.sessionName)
	if err != nil {
		return fmt.Errorf("auth: retrieving session: %w", err)
	}

	session.Values[authKey] = true
	session.Values[stateKey] = state
	session.Values[lastAccessKey] = time.Now()
	session.Options = a.config.Options
	if err := session.Save(r, w); err != nil {
		return fmt.Errorf("auth: saving session: %w", err)
	}
	return nil
}

// Value returns the auth state for this request, or nil if not logged in.
// Must be called after Middleware has run on the request.
func (a *CookieAuth) Value(r *http.Request) any {
	return r.Context().Value(stateCtxKey)
}

// Clear deletes authentication state for this instance and expires the
// session cookie on the client.
func (a *CookieAuth) Clear(w http.ResponseWriter, r *http.Request) {
	if a == nil || a.store == nil {
		return
	}

	session, err := a.store.Get(r, a.sessionName)
	if err == nil {
		clear(session.Values)
	}
	a.expire(w)
}

// Skipped reports whether this request skipped cookie authentication.
// Must be called after Middleware has run on the request.
func (a *CookieAuth) Skipped(r *http.Request) bool {
	value := r.Context().Value(authSkippedKey)
	skipped, ok := value.(bool)
	return ok && skipped
}
