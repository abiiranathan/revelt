package auth

import (
	"context" // for context.WithValue
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/abiiranathan/revelt"
	"github.com/golang-jwt/jwt/v5"
)

// claimsCtxKey and skippedCtxKey are unexported context key types local to
// this file, distinct from the ctxKey type used by cookie.go so the two
// auth mechanisms cannot accidentally collide on the same key value.
type claimsCtxKey string
type jwtSkippedCtxKey string

const (
	jwtClaimsKey     claimsCtxKey     = "jwt_claims_key"
	tokenPrefix      string           = "Bearer "
	jwtAuthIsSkipped jwtSkippedCtxKey = "jwt_auth_skipped"
)

// JWT creates a JWT middleware with the given secret and options.
// If skipFunc returns true for a request, authentication is skipped.
func JWT(secret string, skipFunc func(r *http.Request) bool) func(revelt.HandlerFunc) revelt.HandlerFunc {
	return func(next revelt.HandlerFunc) revelt.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) error {
			if skipFunc != nil && skipFunc(r) {
				ctx := context.WithValue(r.Context(), jwtAuthIsSkipped, true)
				return next(w, r.WithContext(ctx))
			}

			// Extract the JWT token from the Authorization header, stripping
			// the "Bearer " prefix and any surrounding whitespace.
			tokenString := r.Header.Get("Authorization")
			tokenString = strings.TrimPrefix(tokenString, tokenPrefix)
			tokenString = strings.TrimSpace(tokenString)

			if tokenString == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return nil
			}

			claims, err := VerifyJWToken(secret, tokenString)
			if err != nil {
				w.WriteHeader(http.StatusUnauthorized)
				return nil
			}

			ctx := context.WithValue(r.Context(), jwtClaimsKey, claims)
			return next(w, r.WithContext(ctx))
		}
	}
}

// CreateJWTToken creates a JWT token with the given payload and expiry duration.
// The token is signed with the secret key using HMAC SHA-256.
func CreateJWTToken(secret string, payload any, exp time.Duration) (string, error) {
	if secret == "" {
		return "", fmt.Errorf("auth: secret key must not be empty")
	}

	token := jwt.New(jwt.SigningMethodHS256)
	claims := token.Claims.(jwt.MapClaims)
	claims["payload"] = payload
	claims["exp"] = time.Now().Add(exp).Unix()

	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("auth: signing JWT: %w", err)
	}
	return signed, nil
}

// VerifyJWToken verifies the given JWT token with the secret key.
// Returns the claims if the token is valid, otherwise an error. The token
// is verified using the HMAC256 algorithm. The default claims are stored in
// the "payload" key and the expiry time in the "exp" key.
func VerifyJWToken(secret, tokenString string) (jwt.MapClaims, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
		// Validate the signing method to guard against algorithm-confusion
		// attacks (e.g. an attacker supplying "alg": "none").
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, fmt.Errorf("auth: parsing JWT: %w", err)
	}

	if !token.Valid {
		return nil, fmt.Errorf("auth: invalid or expired token")
	}

	return token.Claims.(jwt.MapClaims), nil
}

// JwtClaims returns the JWT claims stored on the request context. It should
// be called after JWT verification middleware has run for the request.
func JwtClaims(r *http.Request) (jwt.MapClaims, error) {
	if claims, ok := r.Context().Value(jwtClaimsKey).(jwt.MapClaims); ok {
		return claims, nil
	}
	return nil, fmt.Errorf("auth: invalid or missing JWT claims")
}

// JWTAuthSkipped reports whether JWT authentication was skipped for the request.
func JWTAuthSkipped(r *http.Request) bool {
	value := r.Context().Value(jwtAuthIsSkipped)
	skipped, ok := value.(bool)
	return ok && skipped
}
