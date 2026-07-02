// Package security provides security header middleware for the revelt framework.
package security

import (
	"fmt"
	"net/http"

	"github.com/abiiranathan/revelt"
)

// Config defines the configuration for the Security middleware.
type Config struct {
	// XSSProtection sets the X-XSS-Protection header value. This header
	// enables the Cross-Site Scripting (XSS) filter built into most recent
	// web browsers. Recommended: "1; mode=block".
	XSSProtection string

	// ContentTypeNosniff sets the X-Content-Type-Options header value. It
	// prevents the browser from MIME-sniffing a response away from the
	// declared content-type. Recommended: "nosniff".
	ContentTypeNosniff string

	// XFrameOptions sets the X-Frame-Options header value. It indicates
	// whether a browser should be allowed to render this page in a
	// <frame>, <iframe>, <embed>, or <object>. Recommended: "SAMEORIGIN"
	// (allows framing only from the same origin) or "DENY" (blocks all
	// framing).
	XFrameOptions string

	// HSTSMaxAge sets the Strict-Transport-Security header's max-age value
	// in seconds. Tells the browser to only access the site over HTTPS for
	// this duration. Default 0 disables HSTS. Recommended: 31536000 (1 year).
	HSTSMaxAge int

	// HSTSExcludeSubdomains, if true, excludes subdomains from the HSTS
	// policy. Default false includes subdomains when HSTS is enabled.
	HSTSExcludeSubdomains bool

	// HSTSPreload, if true, adds the "preload" directive to the HSTS
	// header, permitting submission to browsers' HSTS preload lists.
	HSTSPreload bool

	// ContentSecurityPolicy sets the Content-Security-Policy header value.
	// CSP restricts which resources (scripts, styles, images, etc.) the
	// browser is allowed to load, mitigating XSS and data-injection
	// attacks. Example: "default-src 'self'". Default "" disables CSP.
	ContentSecurityPolicy string

	// ReferrerPolicy sets the Referrer-Policy header value, controlling how
	// much referrer information is sent with outgoing requests. Example:
	// "no-referrer" or "strict-origin-when-cross-origin". Default ""
	// disables the header.
	ReferrerPolicy string
}

// DefaultConfig returns the recommended baseline security header configuration.
func DefaultConfig() Config {
	return Config{
		XSSProtection:      "1; mode=block",
		ContentTypeNosniff: "nosniff",
		XFrameOptions:      "SAMEORIGIN",
	}
}

// New creates security-header middleware using DefaultConfig.
func New() func(revelt.HandlerFunc) revelt.HandlerFunc {
	return WithConfig(DefaultConfig())
}

// WithConfig creates security-header middleware with the given
// configuration, setting HTTP response headers that protect against common
// web attacks (XSS, MIME sniffing, clickjacking, protocol downgrade).
func WithConfig(config Config) func(revelt.HandlerFunc) revelt.HandlerFunc {
	if config.XSSProtection == "" {
		config.XSSProtection = "1; mode=block"
	}
	if config.ContentTypeNosniff == "" {
		config.ContentTypeNosniff = "nosniff"
	}
	if config.XFrameOptions == "" {
		config.XFrameOptions = "SAMEORIGIN"
	}

	return func(next revelt.HandlerFunc) revelt.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) error {
			h := w.Header()

			if config.XSSProtection != "" {
				h.Set("X-XSS-Protection", config.XSSProtection)
			}
			if config.ContentTypeNosniff != "" {
				h.Set("X-Content-Type-Options", config.ContentTypeNosniff)
			}
			if config.XFrameOptions != "" {
				h.Set("X-Frame-Options", config.XFrameOptions)
			}
			if config.HSTSMaxAge > 0 {
				hsts := fmt.Sprintf("max-age=%d", config.HSTSMaxAge)
				if !config.HSTSExcludeSubdomains {
					hsts += "; includeSubDomains"
				}
				if config.HSTSPreload {
					hsts += "; preload"
				}
				h.Set("Strict-Transport-Security", hsts)
			}
			if config.ContentSecurityPolicy != "" {
				h.Set("Content-Security-Policy", config.ContentSecurityPolicy)
			}
			if config.ReferrerPolicy != "" {
				h.Set("Referrer-Policy", config.ReferrerPolicy)
			}

			return next(w, r)
		}
	}
}
