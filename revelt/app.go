// Package revelt provides infrastructure for server-side rendering of Svelte
// and React components via supervised Node.js sidecar processes. This file
// exposes the high-level App API that most users interact with directly.
package revelt

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// sidecarScript is the compiled Node.js render server filename.
	sidecarScript = "render-server.cjs"

	// clientDir is the subdirectory under OutDir holding browser assets.
	clientDir = "client"

	// shutdownTimeout is how long Run waits for in-flight requests to drain.
	shutdownTimeout = 10 * time.Second

	// immutableMaxAge is the Cache-Control max-age for content-hashed assets.
	// One year — effectively immutable since the hash changes with content.
	immutableMaxAge = 31_536_000

	// indexMaxAge is the Cache-Control max-age for index.html.
	// Zero forces revalidation on every navigation without a round-trip when
	// the ETag matches.
	indexMaxAge = 0
)

// HandlerFunc is like http.HandlerFunc but returns an error. Returning a
// non-nil error causes the App's ErrorHandler to write the HTTP response.
// This keeps route handlers free of repetitive http.Error boilerplate while
// remaining compatible with the standard library's handler conventions.
//
// Example:
//
//	app.HandleFunc("/users/{id}", func(w http.ResponseWriter, r *http.Request) error {
//	    user, err := db.Find(r.PathValue("id"))
//	    if err != nil {
//	        return err // ErrorHandler writes 500; no duplicate w.WriteHeader call
//	    }
//	    return revelt.JSON(w, user)
//	})
type HandlerFunc func(http.ResponseWriter, *http.Request) error

// ErrorHandlerFunc is called whenever a HandlerFunc returns a non-nil error.
// Implementations must write a complete HTTP response (status + body).
// The default implementation logs the error and writes a plain-text 500.
type ErrorHandlerFunc func(http.ResponseWriter, *http.Request, error)

// adapt wraps a HandlerFunc in a plain http.Handler. The returned handler calls
// fn and, on error, delegates to errHandler to write the response.
func adapt(fn HandlerFunc, errHandler ErrorHandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			errHandler(w, r, err)
		}
	})
}

// defaultErrorHandler logs err and responds with a 500 Internal Server Error.
// It is the fallback when no ErrorHandler is configured via WithErrorHandler.
func defaultErrorHandler(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("[revelt] handler error %s %s: %v\n", r.Method, r.URL.Path, err)
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}

// App is the top-level revelt application. It owns the renderer, the HTTP
// mux, and the HTTP server. Most users construct one with NewApp, register
// routes via Handle/HandleFunc, and then call Run.
//
// App is not safe for concurrent configuration after Run has been called.
type App struct {
	renderer   *Renderer
	cfg        *ProjectConfig
	mux        *http.ServeMux
	srv        *http.Server
	opts       appOptions
	ctx        context.Context    // merged user + signal context; cancelled on shutdown
	stopSignal context.CancelFunc // releases the signal.NotifyContext resources
	cancel     context.CancelFunc // cancels ctx; called in Run's cleanup
	staticFs   http.FileSystem    // File system containing client-side static assets
}

// appOptions holds functional-option overrides for App behaviour.
type appOptions struct {
	// readTimeout is the HTTP server read timeout.
	readTimeout time.Duration

	// writeTimeout is the HTTP server write timeout.
	writeTimeout time.Duration

	// idleTimeout is the HTTP server idle (keep-alive) timeout.
	idleTimeout time.Duration

	// shutdownTimeout overrides the default drain period on SIGINT/SIGTERM.
	shutdownTimeout time.Duration

	// middleware is a chain of http.Handler wrappers applied to the root mux.
	middleware []func(http.Handler) http.Handler

	// logger is used for access and error logging. Defaults to log.Default().
	logger *log.Logger

	// errorHandler is called whenever a HandlerFunc returns a non-nil error.
	// Defaults to defaultErrorHandler.
	errorHandler ErrorHandlerFunc

	// disableCompression disables on-the-fly gzip compression for static assets.
	disableCompression bool
}

// AppOption configures an App during construction.
type AppOption func(*appOptions)

// WithReadTimeout sets the HTTP server's read timeout.
func WithReadTimeout(d time.Duration) AppOption {
	return func(o *appOptions) { o.readTimeout = d }
}

// WithWriteTimeout sets the HTTP server's write timeout.
func WithWriteTimeout(d time.Duration) AppOption {
	return func(o *appOptions) { o.writeTimeout = d }
}

// WithIdleTimeout sets the HTTP server's idle connection timeout.
func WithIdleTimeout(d time.Duration) AppOption {
	return func(o *appOptions) { o.idleTimeout = d }
}

// WithShutdownTimeout overrides how long Run waits for in-flight requests to
// complete before forcibly closing connections.
func WithShutdownTimeout(d time.Duration) AppOption {
	return func(o *appOptions) { o.shutdownTimeout = d }
}

// WithMiddleware appends one or more handler wrappers to the middleware chain.
// Middleware is applied in declaration order (first declared = outermost).
func WithMiddleware(mw ...func(http.Handler) http.Handler) AppOption {
	return func(o *appOptions) { o.middleware = append(o.middleware, mw...) }
}

// WithLogger sets a custom logger for the App. Defaults to log.Default().
func WithLogger(l *log.Logger) AppOption {
	return func(o *appOptions) { o.logger = l }
}

// WithErrorHandler replaces the default error handler used when a HandlerFunc
// returns a non-nil error. The supplied function must write a complete HTTP
// response (status line + body).
//
// Use this to translate domain errors to specific status codes, e.g.:
//
//	app, _ := revelt.NewApp(ctx, "revelt.json",
//	    revelt.WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) {
//	        var notFound *store.NotFoundError
//	        if errors.As(err, &notFound) {
//	            http.Error(w, notFound.Error(), http.StatusNotFound)
//	            return
//	        }
//	        http.Error(w, http.StatusText(500), 500)
//	    }),
//	)
func WithErrorHandler(fn ErrorHandlerFunc) AppOption {
	return func(o *appOptions) { o.errorHandler = fn }
}

// WithoutCompression disables on-the-fly gzip compression for static assets.
func WithoutCompression() AppOption {
	return func(o *appOptions) { o.disableCompression = true }
}

// NewApp loads revelt.json from configPath, starts the Node.js worker pool,
// registers the static asset handler, and returns a fully initialised App.
//
// The supplied context expresses the caller's own cancellation domain
// (e.g. a test timeout or a parent service). NewApp layers SIGINT/SIGTERM
// handling on top of it internally, so callers never need to construct a
// signal-aware context themselves. The App shuts down when either the
// caller's context is cancelled or a termination signal is received,
// whichever comes first.
//
// Call Run (which takes no context) to start the HTTP server.
func NewApp(ctx context.Context, configPath string, staticFS http.FileSystem, opts ...AppOption) (*App, error) {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("revelt.NewApp: loading config: %w", err)
	}

	sidecar := filepath.Join(cfg.OutDir, sidecarScript)

	o := appOptions{
		readTimeout:     5 * time.Second,
		writeTimeout:    30 * time.Second,
		idleTimeout:     120 * time.Second,
		shutdownTimeout: shutdownTimeout,
		logger:          log.Default(),
		errorHandler:    defaultErrorHandler,
	}
	for _, opt := range opts {
		opt(&o)
	}

	// Layer signal handling on top of the caller's context. The App cancels
	// on whichever fires first: the caller's ctx, SIGINT, or SIGTERM.
	// stopSignal must be called to release the signal.NotifyContext resources.
	sigCtx, stopSignal := signalNotifyContext()

	mergedCtx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-sigCtx.Done():
			cancel()
		case <-ctx.Done():
			cancel()
		}
	}()

	renderer, err := New(mergedCtx, sidecar,
		WithWorkers(cfg.Workers),
		WithRenderTimeout(time.Duration(cfg.TimeoutMS)*time.Millisecond),
		WithProjectConfig(cfg),
	)
	if err != nil {
		cancel()
		stopSignal()
		return nil, fmt.Errorf("revelt.NewApp: starting renderer: %w", err)
	}

	app := &App{
		renderer:   renderer,
		cfg:        cfg,
		mux:        http.NewServeMux(),
		opts:       o,
		ctx:        mergedCtx,
		stopSignal: stopSignal,
		cancel:     cancel,
		staticFs:   staticFS,
	}

	// Register the static asset handler automatically. Users never need to
	// wire this up themselves.
	app.registerStaticHandler()

	return app, nil
}

// ---------------------------------------------------------------------------
// Route registration
// ---------------------------------------------------------------------------

// Handle registers a HandlerFunc for the given pattern. If the handler returns
// a non-nil error the App's ErrorHandler is invoked to write the response.
// Pattern syntax follows [http.ServeMux].
func (a *App) Handle(pattern string, handler HandlerFunc) {
	a.mux.Handle(pattern, adapt(handler, a.opts.errorHandler))
}

// HandleFunc registers a HandlerFunc function for the given pattern.
// If the handler returns a non-nil error the App's ErrorHandler is invoked.
func (a *App) HandleFunc(pattern string, handler HandlerFunc) {
	a.mux.Handle(pattern, adapt(handler, a.opts.errorHandler))
}

// HandleStd registers a standard library http.Handler for the given pattern.
// Use this to mount third-party handlers or middleware-wrapped sub-routers
// that are not expressed as revelt.HandlerFunc.
func (a *App) HandleStd(pattern string, handler http.Handler) {
	a.mux.Handle(pattern, handler)
}

// HandleFuncStd registers a standard library handler function for the given
// pattern. Use this for handlers that manage their own error responses.
func (a *App) HandleFuncStd(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	a.mux.HandleFunc(pattern, handler)
}

// RegisterHealthEndpoints mounts /healthz (liveness) and /readyz (readiness)
// on the App's mux. These are not registered by default so that applications
// with existing health infrastructure are not forced to use revelt's.
//
// Import the health sub-package if you need to mount them on a different mux.
func (a *App) RegisterHealthEndpoints() {
	// Inline the health handler logic here to avoid a circular import between
	// revelt and health. The health package accepts a poolStatter interface
	// that *Renderer already satisfies.
	a.mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSONResponse(w, http.StatusOK, map[string]any{
			"status":  "ok",
			"workers": a.renderer.Stats(),
		})
	})

	a.mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		stats := a.renderer.Stats()
		alive := 0
		for _, s := range stats {
			if s.Alive {
				alive++
			}
		}

		status := "ok"
		code := http.StatusOK
		if alive == 0 {
			status = "degraded"
			code = http.StatusServiceUnavailable
		}

		writeJSONResponse(w, code, map[string]any{
			"status":        status,
			"alive_workers": alive,
			"total_workers": len(stats),
			"workers":       stats,
		})
	})
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// RenderComponent renders a single named component and returns its HTML and
// head fragments. It is a thin convenience wrapper around the underlying
// Renderer.Render so callers do not need to hold a separate renderer reference.
func (a *App) RenderComponent(ctx context.Context, in RenderInput) (RenderOutput, error) {
	return a.renderer.Render(ctx, in)
}

// PageBuilder assembles a full HTML page from a Go template and a set of
// rendered component slots. Obtain one with App.NewPage.
type PageBuilder struct {
	app      *App
	template string        // raw HTML template content
	slots    []slotRequest // ordered list of component render requests
}

// slotRequest pairs a Go template key with a component render request.
type slotRequest struct {
	// key is the template variable name (e.g. "Header" maps to {{ .Header }}).
	key string
	// input is forwarded verbatim to Renderer.Render.
	input RenderInput
}

// NewPage returns a PageBuilder that uses the application's default
// index.html (from OutDir/client/index.html) as its template.
// Call Slot to add components, then Render to write the response.
func (a *App) NewPage() *PageBuilder {
	return &PageBuilder{app: a}
}

// NewPageWithTemplate returns a PageBuilder that uses the supplied raw HTML
// string as its template instead of the default index.html. This is useful
// when serving routes that need a different layout from the default.
func (a *App) NewPageWithTemplate(tmpl string) *PageBuilder {
	return &PageBuilder{app: a, template: tmpl}
}

// Slot registers a component to be server-rendered and injected into the Go
// template under the given key. The key must match a {{ .Key }} placeholder
// in the HTML template.
//
// Components are rendered concurrently when Render is called; declaration order
// does not determine execution order, only template substitution order.
//
// Slot returns the receiver so calls can be chained.
func (b *PageBuilder) Slot(key, component string, props map[string]any) *PageBuilder {
	b.slots = append(b.slots, slotRequest{
		key:   key,
		input: RenderInput{Component: component, Props: props},
	})
	return b
}

// Render concurrently renders all registered component slots, merges the
// results into the HTML template, and writes the final page to w.
//
// On the first call within a request, the default template is read from disk.
// The caller is responsible for writing an appropriate Content-Type header
// before calling Render if they need to override the default
// "text/html; charset=utf-8".
func (b *PageBuilder) Render(ctx context.Context, w http.ResponseWriter) error {
	tmplContent, err := b.resolveTemplate()
	if err != nil {
		return fmt.Errorf("revelt page: resolving template: %w", err)
	}

	// Render all component slots concurrently.
	type result struct {
		key string
		out RenderOutput
		err error
	}

	results := make([]result, len(b.slots))
	var wg sync.WaitGroup
	wg.Add(len(b.slots))

	for i, s := range b.slots {
		go func() {
			defer wg.Done()
			out, err := b.app.renderer.Render(ctx, s.input)
			results[i] = result{key: s.key, out: out, err: err}
		}()
	}
	wg.Wait()

	// Collect rendered HTML into the template data map.
	data := make(map[string]any, len(b.slots))
	for _, res := range results {
		if res.err != nil {
			return fmt.Errorf("revelt page: rendering slot %q: %w", res.key, res.err)
		}
		data[res.key] = template.HTML(res.out.HTML) //nolint:gosec // HTML is renderer-generated
	}

	t, err := template.New("page").Parse(tmplContent)
	if err != nil {
		return fmt.Errorf("revelt page: parsing template: %w", err)
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return fmt.Errorf("revelt page: executing template: %w", err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// index.html must not be cached immutably — it references hashed asset URLs
	// that change on each build.
	w.Header().Set("Cache-Control", fmt.Sprintf("no-cache, max-age=%d", indexMaxAge))
	_, err = buf.WriteTo(w)
	return err
}

// resolveTemplate returns the raw HTML template string for this builder. If a
// template was supplied via NewPageWithTemplate it is returned as-is; otherwise
// the default index.html is read via the app's static filesystem, which
// transparently falls back to the embedded build when running from a
// deployed binary with no frontend source tree present.
func (b *PageBuilder) resolveTemplate() (string, error) {
	if b.template != "" {
		return b.template, nil
	}

	f, err := b.app.staticFs.Open("index.html")
	if err != nil {
		return "", fmt.Errorf("opening index.html: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return "", fmt.Errorf("reading index.html: %w", err)
	}
	return string(data), nil
}

// ---------------------------------------------------------------------------
// Static assets
// ---------------------------------------------------------------------------

// registerStaticHandler mounts an asset-serving handler at cfg.StaticPrefix.
// It wraps http.FileServer with:
//   - Immutable Cache-Control for content-hashed chunk files (chunks/**).
//   - No-cache for the entry file (client-<hash>.js) and CSS.
//   - Optional on-the-fly gzip compression.
func (a *App) registerStaticHandler() {
	// Safe fallback to disk-based directory serving if no filesystem was passed
	if a.staticFs == nil {
		assetsDir := filepath.Join(a.cfg.OutDir, clientDir)
		a.staticFs = http.Dir(assetsDir)
	}

	fs := http.FileServer(a.staticFs)
	base := http.StripPrefix(a.cfg.StaticPrefix, fs)

	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, a.cfg.StaticPrefix)

		// Content-hashed shared chunks under chunks/ are safe to cache forever.
		if strings.HasPrefix(path, "chunks/") {
			w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d, immutable", immutableMaxAge))
		} else {
			// Entry file and CSS: allow caching but require revalidation.
			w.Header().Set("Cache-Control", "no-cache")
		}

		if !a.opts.disableCompression && acceptsGzip(r) {
			gz := gzipResponseWriter{ResponseWriter: w, buf: &bytes.Buffer{}}
			base.ServeHTTP(&gz, r)
			if err := gz.flush(w); err != nil {
				a.opts.logger.Printf("[revelt] gzip flush error: %v", err)
			}
			return
		}

		base.ServeHTTP(w, r)
	})

	a.mux.Handle(a.cfg.StaticPrefix, handler)
}

// ---------------------------------------------------------------------------
// gzip support
// ---------------------------------------------------------------------------

// gzipResponseWriter buffers the response body so it can be compressed before
// being written to the underlying connection.
type gzipResponseWriter struct {
	http.ResponseWriter
	buf        *bytes.Buffer
	statusCode int
}

func (g *gzipResponseWriter) WriteHeader(code int) { g.statusCode = code }

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	return g.buf.Write(b)
}

// flush compresses the buffered body and writes the full response to the
// underlying ResponseWriter.
func (g *gzipResponseWriter) flush(w http.ResponseWriter) error {
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Del("Content-Length") // length changes after compression

	if g.statusCode != 0 {
		w.WriteHeader(g.statusCode)
	}

	gz, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
	if err != nil {
		return err
	}
	defer gz.Close()

	_, err = io.Copy(gz, g.buf)
	return err
}

// acceptsGzip reports whether the client declared support for gzip encoding.
func acceptsGzip(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")
}

// ---------------------------------------------------------------------------
// Server lifecycle
// ---------------------------------------------------------------------------

// Run starts the HTTP server on the port declared in revelt.json, blocks until
// the App's context is cancelled (either by a termination signal or by the
// context passed to NewApp), then performs a graceful shutdown.
//
// Run takes no context argument: the App was already bound to a merged
// signal+caller context at construction time. The typical calling pattern is:
//
//	app, _ := revelt.NewApp(context.Background(), "revelt.json")
//	app.RegisterHealthEndpoints()
//	app.HandleFunc("/", myHandler)
//	app.Run()
func (a *App) Run() {
	// Apply middleware chain (outermost first).
	var root http.Handler = a.mux
	for i := len(a.opts.middleware) - 1; i >= 0; i-- {
		root = a.opts.middleware[i](root)
	}

	a.srv = &http.Server{
		Addr:         fmt.Sprintf(":%d", a.cfg.Port),
		Handler:      root,
		ReadTimeout:  a.opts.readTimeout,
		WriteTimeout: a.opts.writeTimeout,
		IdleTimeout:  a.opts.idleTimeout,
	}

	a.opts.logger.Printf("[revelt] listening on http://localhost:%d", a.cfg.Port)

	go func() {
		if err := a.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			a.opts.logger.Fatalf("[revelt] server error: %v", err)
		}
	}()

	<-a.ctx.Done()
	a.opts.logger.Println("[revelt] shutdown signal received, draining…")

	// Release the OS signal subscription regardless of why we are shutting down.
	a.stopSignal()

	// Close the renderer first so in-flight SSR calls to Node get a chance to
	// finish before we cut the HTTP connection.
	if err := a.renderer.Close(); err != nil {
		a.opts.logger.Printf("[revelt] renderer close error: %v", err)
	}

	sdCtx, sdCancel := context.WithTimeout(context.Background(), a.opts.shutdownTimeout)
	defer sdCancel()

	if err := a.srv.Shutdown(sdCtx); err != nil {
		a.opts.logger.Printf("[revelt] graceful shutdown error: %v", err)
	}

	// Ensure the merged context is fully cancelled so any goroutine still
	// selecting on a.ctx sees Done closed.
	a.cancel()

	a.opts.logger.Println("[revelt] stopped.")
}

// writeJSONResponse is a thin JSON response helper used by the inline health
// handlers. It avoids importing encoding/json into every call site.
func writeJSONResponse(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = jsonEncode(w, v)
}
