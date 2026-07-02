package auth_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/abiiranathan/revelt"
	"github.com/abiiranathan/revelt/middleware/auth"
	"github.com/gorilla/securecookie"
	"github.com/gorilla/sessions"
)

// User is the session state type used across the auth_test package.
type User struct {
	Username string
	Password string
}

// errorCallback is a CookieConfig.ErrorHandler that writes a bare 401.
func errorCallback(w http.ResponseWriter, _ *http.Request) error {
	w.WriteHeader(http.StatusUnauthorized)
	return nil
}

// skipAuth exempts the /login path from cookie authentication.
func skipAuth(r *http.Request) bool {
	return r.URL.Path == "/login"
}

// adapt converts a revelt.HandlerFunc into a standard http.HandlerFunc,
// writing a 500 response if the handler returns an error. This is the same
// glue App.Handle performs internally in revelt, reimplemented here so
// tests can exercise middleware without constructing a full App.
func adapt(h revelt.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// mux builds an *http.ServeMux from a set of patterns to handlers, wrapping
// each handler with mw before adapting it. Used to stand in for rex's
// Router across these tests.
func mux(mw func(revelt.HandlerFunc) revelt.HandlerFunc, routes map[string]revelt.HandlerFunc) *http.ServeMux {
	m := http.NewServeMux()
	for pattern, h := range routes {
		m.HandleFunc(pattern, adapt(mw(h)))
	}
	return m
}

// TestCookieMiddleware exercises the full login -> authenticated request ->
// logout -> unauthenticated request lifecycle.
func TestCookieMiddleware(t *testing.T) {
	secretKey := securecookie.GenerateRandomKey(32)
	encryptionKey := securecookie.GenerateRandomKey(32)
	cookieAuth, err := auth.NewCookieAuth("rex_session_name", [][]byte{secretKey, encryptionKey}, User{}, auth.CookieConfig{
		Options: &sessions.Options{
			MaxAge:   int((24 * time.Hour).Seconds()),
			Secure:   false,
			SameSite: http.SameSiteStrictMode,
		},
		ErrorHandler: errorCallback,
		SkipAuth:     skipAuth,
	})
	if err != nil {
		t.Fatalf("failed to initialize cookie auth: %v", err)
	}

	router := mux(cookieAuth.Middleware(), map[string]revelt.HandlerFunc{
		"/login": func(w http.ResponseWriter, r *http.Request) error {
			contentType := r.Header.Get("Content-Type")
			if contentType != "application/x-www-form-urlencoded" && contentType != "multipart/form-data" {
				w.WriteHeader(http.StatusBadRequest)
				return nil
			}

			username := r.FormValue("username")
			password := r.FormValue("password")
			if username == "" || password == "" {
				w.WriteHeader(http.StatusBadRequest)
				return nil
			}

			// Credential validation would happen here in a real handler.
			if err := cookieAuth.SetState(w, r, User{username, password}); err != nil {
				return err
			}
			_, err := w.Write([]byte("Login successful"))
			return err
		},
		"/": func(w http.ResponseWriter, r *http.Request) error {
			state := cookieAuth.Value(r)
			if state == nil {
				t.Fatal("user is not authenticated")
			}
			_, err := fmt.Fprintf(w, "Welcome home: %s", state.(User).Username)
			return err
		},
		"/logout": func(w http.ResponseWriter, r *http.Request) error {
			cookieAuth.Clear(w, r)
			_, err := w.Write([]byte("Logout successful"))
			return err
		},
	})

	form := url.Values{
		"username": {"abiiranathan"},
		"password": {"supersecurepassword"},
	}
	body := form.Encode()

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected status code %d, got %d, body: %s\n", http.StatusOK, w.Result().StatusCode, w.Body.String())
	}

	hdr := w.Header()
	cookies, ok := hdr["Set-Cookie"]
	if !ok || len(cookies) == 0 {
		t.Fatalf("Set-Cookie header missing in response")
	}

	// Perform authenticated request.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Add("Cookie", cookies[0])
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected status code %d, got %d", http.StatusOK, w.Result().StatusCode)
	}

	expected := "Welcome home: abiiranathan"
	if expected != w.Body.String() {
		t.Fatalf("expected %q, got %s\n", expected, w.Body.String())
	}

	// Perform logout.
	req = httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.Header.Add("Cookie", cookies[0])
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected status code %d, got %d", http.StatusOK, w.Result().StatusCode)
	}

	// Perform unauthenticated request.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusUnauthorized {
		t.Errorf("expected status code %d, got %d", http.StatusUnauthorized, w.Result().StatusCode)
	}
}

// TestCookieSlidingWindowRefresh verifies the sliding-window session
// extension behavior: no refresh before the threshold, a refresh once the
// threshold elapses, and eventual expiry of the original cookie if it is
// never refreshed.
func TestCookieSlidingWindowRefresh(t *testing.T) {
	secretKey := securecookie.GenerateRandomKey(32)
	encryptionKey := securecookie.GenerateRandomKey(32)

	// Use a short MaxAge so we can reason about the threshold easily.
	// refreshThreshold = maxAge / 2 = 4s.
	maxAge := 8
	cookieAuth, err := auth.NewCookieAuth("rex_session_name", [][]byte{secretKey, encryptionKey}, User{}, auth.CookieConfig{
		Options: &sessions.Options{
			MaxAge:   maxAge,
			Secure:   false,
			SameSite: http.SameSiteStrictMode,
		},
		ErrorHandler: errorCallback,
		SkipAuth:     skipAuth,
	})
	if err != nil {
		t.Fatalf("failed to initialize cookie auth: %v", err)
	}

	router := mux(cookieAuth.Middleware(), map[string]revelt.HandlerFunc{
		"/login": func(w http.ResponseWriter, r *http.Request) error {
			if err := cookieAuth.SetState(w, r, User{"testuser", "testpass"}); err != nil {
				return err
			}
			_, err := w.Write([]byte("ok"))
			return err
		},
		"/protected": func(w http.ResponseWriter, _ *http.Request) error {
			_, err := w.Write([]byte("ok"))
			return err
		},
	})

	// Helper: perform a request and return the response recorder.
	doRequest := func(method, path string, reqCookies []string) *httptest.ResponseRecorder {
		var req *http.Request
		if method == http.MethodPost && path == "/login" {
			form := url.Values{"username": {"testuser"}, "password": {"testpass"}}
			req = httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		for _, c := range reqCookies {
			req.Header.Add("Cookie", c)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	// Login and grab the initial cookie.
	w := doRequest(http.MethodPost, "/login", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("login failed: %d", w.Code)
	}
	cookies := w.Header()["Set-Cookie"]
	if len(cookies) == 0 {
		t.Fatal("no Set-Cookie header after login")
	}
	firstCookie := cookies[0]

	// --- Invariant 1: No refresh before the threshold is crossed ---
	w = doRequest(http.MethodGet, "/protected", []string{firstCookie})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if _, refreshed := w.Header()["Set-Cookie"]; refreshed {
		t.Error("cookie was refreshed too early: no Set-Cookie should be issued before the threshold")
	}

	// --- Invariant 2: Cookie IS refreshed once the threshold has elapsed ---
	time.Sleep(time.Duration(maxAge/2+1) * time.Second)

	w = doRequest(http.MethodGet, "/protected", []string{firstCookie})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 after threshold sleep, got %d", w.Code)
	}
	refreshedCookies := w.Header()["Set-Cookie"]
	if len(refreshedCookies) == 0 {
		t.Fatal("cookie was NOT refreshed after the threshold elapsed: expected a new Set-Cookie header")
	}
	refreshedCookie := refreshedCookies[0]

	if refreshedCookie == firstCookie {
		t.Error("refreshed cookie is identical to original: session was not actually extended")
	}

	// --- Invariant 3: The refreshed cookie is still valid ---
	w = doRequest(http.MethodGet, "/protected", []string{refreshedCookie})
	if w.Code != http.StatusOK {
		t.Fatalf("refreshed cookie rejected: expected 200, got %d", w.Code)
	}

	// --- Invariant 4: Original cookie remains valid until MaxAge is truly exhausted ---
	w = doRequest(http.MethodGet, "/protected", []string{firstCookie})
	if w.Code != http.StatusOK {
		t.Fatalf("original cookie should still be valid before full MaxAge: got %d", w.Code)
	}

	// --- Invariant 5: Session expires if never refreshed ---
	time.Sleep(time.Duration(maxAge/2+1) * time.Second)

	w = doRequest(http.MethodGet, "/protected", []string{firstCookie})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expired original cookie should be rejected: expected 401, got %d", w.Code)
	}

	// But the refreshed cookie should still work (its MaxAge was reset).
	w = doRequest(http.MethodGet, "/protected", []string{refreshedCookie})
	if w.Code != http.StatusOK {
		t.Errorf("refreshed cookie should still be valid: expected 200, got %d", w.Code)
	}
}

// TestCookieAuthInstanceAPI exercises SetState/Value/Skipped through the
// instance methods directly rather than via the full login flow.
func TestCookieAuthInstanceAPI(t *testing.T) {
	secretKey := securecookie.GenerateRandomKey(32)
	encryptionKey := securecookie.GenerateRandomKey(32)

	cookieAuth, err := auth.NewCookieAuth("instance_session", [][]byte{secretKey, encryptionKey}, User{}, auth.CookieConfig{
		ErrorHandler: errorCallback,
		SkipAuth: func(r *http.Request) bool {
			return r.URL.Path == "/skip" || r.URL.Path == "/login"
		},
	})
	if err != nil {
		t.Fatalf("failed to initialize cookie auth: %v", err)
	}

	router := mux(cookieAuth.Middleware(), map[string]revelt.HandlerFunc{
		"/login": func(w http.ResponseWriter, r *http.Request) error {
			return cookieAuth.SetState(w, r, User{Username: "instance"})
		},
		"/skip": func(w http.ResponseWriter, r *http.Request) error {
			if !cookieAuth.Skipped(r) {
				t.Fatal("expected auth to be skipped")
			}
			_, err := w.Write([]byte("skipped"))
			return err
		},
		"/me": func(w http.ResponseWriter, r *http.Request) error {
			state := cookieAuth.Value(r)
			if state == nil {
				t.Fatal("expected auth state")
			}
			_, err := w.Write([]byte(state.(User).Username))
			return err
		},
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/skip", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/login", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	cookies := w.Header()["Set-Cookie"]
	if len(cookies) == 0 {
		t.Fatal("expected Set-Cookie header")
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Add("Cookie", cookies[0])
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != "instance" {
		t.Fatalf("expected instance, got %s", w.Body.String())
	}
}

// TestCookieAuthMultipleInstances verifies that two independently
// configured CookieAuth instances (different session names and keys) do
// not accept each other's cookies.
func TestCookieAuthMultipleInstances(t *testing.T) {
	keyA := securecookie.GenerateRandomKey(32)
	keyB := securecookie.GenerateRandomKey(32)

	authA, err := auth.NewCookieAuth("session_a", [][]byte{keyA}, User{}, auth.CookieConfig{
		ErrorHandler: errorCallback,
		SkipAuth: func(r *http.Request) bool {
			return r.URL.Path == "/login-a"
		},
	})
	if err != nil {
		t.Fatalf("failed to initialize cookie authA: %v", err)
	}
	authB, err := auth.NewCookieAuth("session_b", [][]byte{keyB}, User{}, auth.CookieConfig{
		ErrorHandler: errorCallback,
		SkipAuth: func(r *http.Request) bool {
			return r.URL.Path == "/login-b"
		},
	})
	if err != nil {
		t.Fatalf("failed to initialize cookie authB: %v", err)
	}

	routerA := mux(authA.Middleware(), map[string]revelt.HandlerFunc{
		"/login-a": func(w http.ResponseWriter, r *http.Request) error {
			return authA.SetState(w, r, User{Username: "user-a"})
		},
		"/protected-a": func(w http.ResponseWriter, r *http.Request) error {
			_, err := w.Write([]byte(authA.Value(r).(User).Username))
			return err
		},
	})

	routerB := mux(authB.Middleware(), map[string]revelt.HandlerFunc{
		"/login-b": func(w http.ResponseWriter, r *http.Request) error {
			return authB.SetState(w, r, User{Username: "user-b"})
		},
		"/protected-b": func(w http.ResponseWriter, r *http.Request) error {
			_, err := w.Write([]byte(authB.Value(r).(User).Username))
			return err
		},
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/login-a", nil)
	routerA.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	cookieA := w.Header().Get("Set-Cookie")
	if cookieA == "" {
		t.Fatal("expected cookie for authA")
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/protected-a", nil)
	req.Header.Add("Cookie", cookieA)
	routerA.ServeHTTP(w, req)
	if w.Body.String() != "user-a" {
		t.Fatalf("expected user-a, got %s", w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/protected-b", nil)
	req.Header.Add("Cookie", cookieA)
	routerB.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when reusing authA cookie against authB, got %d", w.Code)
	}
}
