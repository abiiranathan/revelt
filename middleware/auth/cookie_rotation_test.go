package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/abiiranathan/revelt"
	"github.com/abiiranathan/revelt/middleware/auth"
	"github.com/gorilla/securecookie"
	"github.com/gorilla/sessions"
)

// TestCookieRotation verifies that a cookie signed with a since-rotated key
// is rejected (rather than silently trusted) and that the server responds
// with a Set-Cookie header expiring the now-invalid client cookie, so the
// browser discards it and the login flow can be re-initiated cleanly.
func TestCookieRotation(t *testing.T) {
	// 1. Initialize with Key A.
	keyA := securecookie.GenerateRandomKey(32)
	sessionName := "rotation_test_session"

	config := auth.CookieConfig{
		Options: &sessions.Options{
			Path:     "/",
			MaxAge:   3600,
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request) error {
			w.WriteHeader(http.StatusUnauthorized)
			_, err := w.Write([]byte("Unauthorized"))
			return err
		},
		SkipAuth: func(r *http.Request) bool {
			return r.URL.Path == "/login"
		},
	}
	authA, err := auth.NewCookieAuth(sessionName, [][]byte{keyA}, User{}, config)
	if err != nil {
		t.Fatalf("failed to initialize cookie authA: %v", err)
	}

	routerA := mux(authA.Middleware(), map[string]revelt.HandlerFunc{
		"/login": func(w http.ResponseWriter, r *http.Request) error {
			return authA.SetState(w, r, User{Username: "test"})
		},
		"/protected": func(w http.ResponseWriter, _ *http.Request) error {
			_, err := w.Write([]byte("Protected Content"))
			return err
		},
	})

	// 2. Obtain a valid cookie signed with Key A.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	routerA.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("login failed: %d", w.Code)
	}

	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no cookies returned")
	}
	validCookie := cookies[0]

	// 3. Rotate keys: create a new auth instance with Key B only.
	keyB := securecookie.GenerateRandomKey(32)
	authB, err := auth.NewCookieAuth(sessionName, [][]byte{keyB}, User{}, config)
	if err != nil {
		t.Fatalf("failed to initialize cookie authB: %v", err)
	}

	routerB := mux(authB.Middleware(), map[string]revelt.HandlerFunc{
		"/protected": func(w http.ResponseWriter, _ *http.Request) error {
			_, err := w.Write([]byte("Protected Content"))
			return err
		},
	})

	// 4. Request /protected with the Key-A-signed cookie against routerB.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Add("Cookie", validCookie.String())
	routerB.ServeHTTP(w, req)

	// 5. Expect 401 Unauthorized.
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized after rotation, got %d", w.Code)
	}

	// 6. Expect a Set-Cookie header that expires the invalid session cookie.
	foundExpiration := false
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionName && c.MaxAge < 0 {
			foundExpiration = true
			break
		}
	}
	if !foundExpiration {
		t.Error("expected Set-Cookie header to expire the invalid session cookie")
	}
}
