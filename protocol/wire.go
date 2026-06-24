// Package protocol defines the newline-delimited JSON wire format exchanged
// between the Go host and the Node.js sidecar render processes.
//
// Each message is a single JSON object terminated by a newline character ('\n'),
// which gives unambiguous framing over an unbuffered pipe without any length
// prefix. Both the request envelope and response envelope carry a request ID so
// that a single stdin/stdout pair can be multiplexed across concurrent callers.
package protocol

// RequestID is a monotonically increasing identifier that correlates a request
// to its response when multiple goroutines share one Node process.
type RequestID uint64

// RenderRequest is the JSON envelope written to the Node process's stdin.
type RenderRequest struct {
	// ID correlates this request to its response. The Node side echoes it back.
	ID RequestID `json:"id"`

	// Component is the import path or registered name of the component to render.
	// How this is resolved is left to the render-server script (e.g. a registry
	// map keyed by this string).
	Component string `json:"component"`

	// Props is an arbitrary JSON object forwarded verbatim to the component as
	// its server-side props. A nil map is serialised as JSON null; the Node side
	// should treat null and {} equivalently.
	Props map[string]any `json:"props"`
}

// RenderResponse is the JSON envelope read from the Node process's stdout.
type RenderResponse struct {
	// ID echoes the RequestID from the corresponding RenderRequest.
	ID RequestID `json:"id"`

	// HTML is the rendered component markup (the body fragment).
	HTML string `json:"html"`

	// Head contains any <head> injections the component declared (meta tags,
	// links, title, etc.). May be empty.
	Head string `json:"head"`

	// Error is non-empty when the Node side reported a render failure. When
	// Error is set, HTML and Head should be treated as undefined.
	Error string `json:"error,omitempty"`
}
