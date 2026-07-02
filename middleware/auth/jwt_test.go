package auth_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abiiranathan/revelt/middleware/auth"
)

// TestCreateJWTToken verifies that CreateJWTToken produces a well-formed
// three-segment JWT (header.payload.signature).
func TestCreateJWTToken(t *testing.T) {
	payload := "userId"
	duration := time.Minute * 30

	token, err := auth.CreateJWTToken("supersecret", payload, duration)
	if err != nil {
		t.Error(err)
	}

	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		t.Fatalf("invalid JWT token: %s\n", token)
	}
}

// TestVerifyToken verifies that a token created with CreateJWTToken decodes
// back to its original payload via VerifyJWToken, and that an expired token
// is rejected.
func TestVerifyToken(t *testing.T) {
	payload := "userId"
	duration := time.Minute * 30
	secret := "supersecret"

	token, err := auth.CreateJWTToken(secret, payload, duration)
	if err != nil {
		t.Fatal(err)
	}

	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		t.Fatalf("invalid JWT token: %s\n", token)
	}

	claims, err := auth.VerifyJWToken(secret, token)
	if err != nil {
		t.Fatal(err)
	}

	userID, ok := claims["payload"]
	if !ok || userID != payload {
		t.Fatalf("expected payload %s, got %s", payload, userID)
	}

	// Expired token.
	token, err = auth.CreateJWTToken(secret, payload, time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}

	claims, err = auth.VerifyJWToken(secret, token)
	if err == nil {
		t.Fatalf("expected error for expired token, got nil")
	}

	t.Logf("expected verification error: %v, claims: %v", err, claims)
}

// TestJWTMiddleware exercises the JWT middleware's three states: missing
// Authorization header, a valid Bearer token, and a malformed token.
func TestJWTMiddleware(t *testing.T) {
	payload := "userId"
	duration := time.Minute * 30
	secret := "supersecret"

	token, err := auth.CreateJWTToken(secret, payload, duration)
	if err != nil {
		t.Fatal(err)
	}

	handler := auth.JWT(secret, nil)(func(w http.ResponseWriter, r *http.Request) error {
		claims, err := auth.JwtClaims(r)
		if err != nil {
			t.Fatalf("%s", err.Error())
		}

		id := claims["payload"]
		if id != payload {
			t.Errorf("expected payload to equal %s, got %v", payload, id)
		}

		_, err = w.Write([]byte("Hello"))
		return err
	})

	router := adapt(handler)

	// Request without auth.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	router(w, req)

	if expected := http.StatusUnauthorized; w.Result().StatusCode != expected {
		t.Errorf("expected status code %d, got %d", expected, w.Result().StatusCode)
	}

	// Pass the correct authorization.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	w = httptest.NewRecorder()
	router(w, req)

	if expected := http.StatusOK; w.Result().StatusCode != expected {
		t.Errorf("expected status code %d, got %d", expected, w.Result().StatusCode)
	}

	// Invalid token.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", "invalid token"))
	w = httptest.NewRecorder()
	router(w, req)

	if expected := http.StatusUnauthorized; w.Result().StatusCode != expected {
		t.Errorf("expected status code %d, got %d", expected, w.Result().StatusCode)
	}
}
