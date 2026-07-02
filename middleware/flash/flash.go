// Package flash provides flash message helpers and middleware for the revelt framework.
package flash

import (
	"context" // for context.WithValue
	"encoding/gob"
	"fmt"
	"net/http"
	"strings"

	"github.com/abiiranathan/revelt"
	"github.com/gorilla/securecookie"
	"github.com/gorilla/sessions"
)

// flashMessageStore backs flash message persistence via an encrypted,
// HTTP-only, session-lifetime cookie.
var flashMessageStore sessions.Store

const (
	sessionName     = "flash_messages_session"
	flashMessageKey = "flash_message"
)

// flashCtxKey is the context key under which the retrieved flash message
// (if any) is stashed for the downstream handler.
type flashCtxKey struct{}

// Flash represents a single flash message and its severity classification.
type Flash struct {
	Message string // Message text to display.
	Type    string // Severity: "success", "info", "warning", or "danger".
}

// FlashMessageType identifies the Bootstrap-style severity used for a flash message.
type FlashMessageType int

// Supported flash message severities.
const (
	MessageSuccess FlashMessageType = iota
	MessageInfo
	MessageWarning
	MessageError
)

func init() {
	secret := securecookie.GenerateRandomKey(32)
	store := sessions.NewCookieStore(secret)
	store.Options.Secure = false  // Send both on http & https.
	store.Options.HttpOnly = true // XSS protection: no access from JS.
	store.Options.MaxAge = 0      // Session cookie is deleted when browser is closed.

	flashMessageStore = store
	gob.Register(Flash{})
}

// setFlashMessage sets a flash message for the given key and persists it.
func setFlashMessage(w http.ResponseWriter, r *http.Request, key string, fm Flash) error {
	sess, _ := flashMessageStore.Get(r, sessionName)
	sess.Values[key] = fm
	if err := sess.Save(r, w); err != nil {
		return fmt.Errorf("flash: saving session: %w", err)
	}
	return nil
}

// clearCookie clears the flash session's stored values and expires its cookie.
func clearCookie(w http.ResponseWriter, r *http.Request) error {
	session, _ := flashMessageStore.Get(r, sessionName)
	clear(session.Values)

	cookie, err := r.Cookie(sessionName)
	if err != nil {
		return nil // No cookie present; nothing to expire.
	}

	cookie.MaxAge = -1
	http.SetCookie(w, cookie)

	if err := session.Save(r, w); err != nil {
		return fmt.Errorf("flash: saving cleared session: %w", err)
	}
	return nil
}

// getFlashMessage retrieves and deletes a flash message for the given key.
func getFlashMessage(w http.ResponseWriter, r *http.Request, key string) (Flash, error) {
	sess, _ := flashMessageStore.Get(r, sessionName)
	if message, ok := sess.Values[key]; ok {
		_ = clearCookie(w, r)
		return message.(Flash), nil
	}
	return Flash{}, fmt.Errorf("flash: no flash message found for key %s", key)
}

// FlashMessage stores a flash message in the session, to be displayed on
// the next request. The default message type is MessageError.
func FlashMessage(w http.ResponseWriter, r *http.Request, message string, messageType ...FlashMessageType) error {
	msgType := "danger"

	if len(messageType) > 0 {
		switch messageType[0] {
		case MessageInfo:
			msgType = "info"
		case MessageSuccess:
			msgType = "success"
		case MessageWarning:
			msgType = "warning"
		default:
			msgType = "danger"
		}
	}

	if err := setFlashMessage(w, r, flashMessageKey, Flash{Message: message, Type: msgType}); err != nil {
		return fmt.Errorf("flash: setting flash message: %w", err)
	}
	return nil
}

// Message returns the flash message attached to the request context by
// FlashMessageMiddleware, or the zero Flash if none was set.
func Message(r *http.Request) Flash {
	if fm, ok := r.Context().Value(flashCtxKey{}).(Flash); ok {
		return fm
	}
	return Flash{}
}

// FlashMessageMiddleware is middleware that retrieves any pending flash
// message and attaches it to the request context, retrievable downstream
// via Message(r). Flash messages are queued by calling FlashMessage.
//
// Flash retrieval only runs for requests that accept HTML (Accept:
// text/html or */*), since flash messages are a browser-navigation concept.
func FlashMessageMiddleware() func(revelt.HandlerFunc) revelt.HandlerFunc {
	return func(next revelt.HandlerFunc) revelt.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) error {
			accept := r.Header.Get("Accept")

			if strings.HasPrefix(accept, "text/html") || strings.HasPrefix(accept, "*/*") {
				message, err := getFlashMessage(w, r, flashMessageKey)
				if err == nil && message.Message != "" {
					ctx := context.WithValue(r.Context(), flashCtxKey{}, message)
					r = r.WithContext(ctx)
				}
			}
			return next(w, r)
		}
	}
}
