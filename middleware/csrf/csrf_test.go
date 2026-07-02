package csrf_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/abiiranathan/revelt"
	"github.com/abiiranathan/revelt/middleware/csrf"
	"github.com/gorilla/sessions"
)

// store is a mock session store, retained for API compatibility with
// csrf.New even though the current implementation round-trips the token
// via a dedicated cookie rather than the session store.
var store = sessions.NewCookieStore([]byte("test-secret"))

// testMiddleware simulates an HTTP request against handler, optionally
// carrying a form-encoded body and a single cookie.
func testMiddleware(method, path, body string, cookie *http.Cookie, handler http.HandlerFunc) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if cookie != nil {
		req.AddCookie(cookie)
	}

	resp := httptest.NewRecorder()
	handler(resp, req)
	return resp
}

// testPOSTRequestWithForm simulates a POST request carrying URL-encoded
// form data and an optional cookie.
func testPOSTRequestWithForm(path string, formData url.Values, cookie *http.Cookie, handler http.HandlerFunc) *httptest.ResponseRecorder {
	body := strings.NewReader(formData.Encode())

	req := httptest.NewRequest(http.MethodPost, path, body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if cookie != nil {
		req.AddCookie(cookie)
	}

	resp := httptest.NewRecorder()
	handler(resp, req)
	return resp
}

// TestCSRFTokenGeneration verifies that the middleware sets an HTTP-only,
// secure CSRF cookie on a GET request when none exists yet.
func TestCSRFTokenGeneration(t *testing.T) {
	handler := revelt.Adapt(csrf.New(store, true)(func(w http.ResponseWriter, _ *http.Request) error {
		_, err := w.Write([]byte("OK"))
		if err != nil {
			t.Errorf("failed to write response: %v", err)
		}
		return nil
	}), revelt.DefaultErrorHandler)

	resp := testMiddleware(http.MethodGet, "/", "", nil, handler.ServeHTTP)

	cookies := resp.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected a CSRF cookie to be set")
	}

	csrfCookie := cookies[0]
	if csrfCookie.Name != "csrf_token" {
		t.Errorf("expected cookie name 'csrf_token', got %q", csrfCookie.Name)
	}
	if !csrfCookie.HttpOnly {
		t.Error("cookie should be HTTP-only")
	}
	if !csrfCookie.Secure {
		t.Error("cookie should be secure (use HTTPS)")
	}
}

// TestCSRFTokenValidationSuccess verifies that a POST carrying a form field
// matching the CSRF cookie is accepted.
func TestCSRFTokenValidationSuccess(t *testing.T) {
	token, err := csrf.CreateToken()
	if err != nil {
		t.Fatalf("failed to create token: %v", err)
	}

	cookie := &http.Cookie{Name: "csrf_token", Value: token}

	handler := revelt.Adapt(csrf.New(store, false)(func(w http.ResponseWriter, _ *http.Request) error {
		_, err := w.Write([]byte("OK"))
		if err != nil {
			t.Errorf("failed to write response: %v", err)
		}
		return nil
	}), revelt.DefaultErrorHandler)

	formData := url.Values{}
	formData.Set("csrf_token", token)

	resp := testPOSTRequestWithForm("/submit", formData, cookie, handler.ServeHTTP)

	if resp.Code != http.StatusOK {
		t.Errorf("expected 200 OK response, got %d", resp.Code)
	}
}

// TestCSRFTokenValidationFailure_MissingToken verifies that a POST with no
// CSRF token at all is rejected.
func TestCSRFTokenValidationFailure_MissingToken(t *testing.T) {
	handler := revelt.Adapt(csrf.New(store, false)(func(w http.ResponseWriter, _ *http.Request) error {
		_, err := w.Write([]byte("OK"))
		if err != nil {
			t.Errorf("failed to write response: %v", err)
		}
		return nil
	}), revelt.DefaultErrorHandler)

	resp := testMiddleware(http.MethodPost, "/submit", "", nil, handler.ServeHTTP)

	if resp.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden response, got %d", resp.Code)
	}
}

// TestCSRFTokenValidationFailure_InvalidToken verifies that a POST whose
// form token does not match the cookie token is rejected.
func TestCSRFTokenValidationFailure_InvalidToken(t *testing.T) {
	validToken, err := csrf.CreateToken()
	if err != nil {
		t.Fatalf("failed to create valid token: %v", err)
	}

	mismatchedToken, err := csrf.CreateToken()
	if err != nil {
		t.Fatalf("failed to create mismatched token: %v", err)
	}

	cookie := &http.Cookie{Name: "csrf_token", Value: validToken}

	handler := revelt.Adapt(csrf.New(store, false)(func(w http.ResponseWriter, _ *http.Request) error {
		_, err := w.Write([]byte("OK"))
		if err != nil {
			t.Errorf("failed to write response: %v", err)
		}
		return nil
	}), revelt.DefaultErrorHandler)

	body := "csrf_token=" + mismatchedToken
	resp := testMiddleware(http.MethodPost, "/submit", body, cookie, handler.ServeHTTP)

	if resp.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden response, got %d", resp.Code)
	}
}

// TestSafeMethodsBypassCSRFValidation verifies that GET and HEAD requests
// are never subject to CSRF validation, since they must not mutate state.
func TestSafeMethodsBypassCSRFValidation(t *testing.T) {
	handler := revelt.Adapt(csrf.New(store, false)(func(w http.ResponseWriter, _ *http.Request) error {
		_, err := w.Write([]byte("OK"))
		if err != nil {
			t.Errorf("failed to write response: %v", err)
		}
		return nil
	}), revelt.DefaultErrorHandler)

	safeMethods := []string{http.MethodGet, http.MethodHead}
	for _, method := range safeMethods {
		t.Run(method, func(t *testing.T) {
			resp := testMiddleware(method, "/", "", nil, handler.ServeHTTP)
			if resp.Code != http.StatusOK {
				t.Errorf("expected 200 OK response, got %d", resp.Code)
			}
		})
	}
}
