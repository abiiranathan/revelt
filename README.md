# revelt

> Server-render React and Svelte components directly from Go handlers — with full hydration, island architecture, and zero Node.js server.

---

## What is revelt, and why does it exist?

Most Go developers who want React or Svelte in their app face an awkward choice:

**Option A — Separate frontend app (Vite, Next.js, SvelteKit)**
You end up with two servers, two deployment units, CORS configuration, a proxy layer, and a mental model split across two runtimes. Your Go business logic and your UI live in different processes, communicate over HTTP, and have to be kept in sync manually.

**Option B — Build the SPA, serve the `dist/` folder from Go**
Simpler to deploy, but now you have a fully client-rendered single-page app. First load is a blank screen until JavaScript parses and executes. No SSR, no SEO, no meaningful content in the initial HTML. Every component ships to the browser regardless of whether it needs interactivity.

**revelt is a third path.**

It lets you drop React or Svelte components into any Go web application. Each component is annotated with a rendering mode — server-only, hydrated, or client-only — and revelt does the right thing automatically:

- `@mode ssr` components are rendered to HTML on the server by a Node.js sidecar pool, added to your response, and never sent as JavaScript to the browser at all. Zero client overhead.
- `@mode hydrate` components are server-rendered for instant first paint and then hydrated by the browser bundle so they become interactive.
- `@mode client` components skip SSR entirely and mount in the browser, for anything that requires Web APIs.

The Node.js processes are not a separate HTTP service. They are supervised child processes connected to the Go binary over anonymous pipes, communicating in newline-delimited JSON. They live and die with the parent process. From your infrastructure's perspective, you deploy one binary.

---

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│                       Go HTTP Server                         │
│                                                              │
│   app.NewPage()                                              │
│     .Slot("Header",     "Header",     props)                 │
│     .Slot("Counter",    "Counter",    props)                 │
│     .Slot("ClientChart","ClientChart",props)                 │
│     .Render(ctx, w)                                          │
│           │ concurrent renders                               │
│     ┌─────▼─────────────────────────────────┐                │
│     │           Pool  (round-robin)         │                │
│     │  ┌────────┐  ┌────────┐  ┌────────┐   │                │
│     │  │worker 0│  │worker 1│  │worker 2│   │                │
│     │  └───┬────┘  └───┬────┘  └───┬────┘   │                │
│     └───────┼───────────┼───────────┼───────┘                │
└─────────────┼───────────┼───────────┼──────────────────────--┘
              │           │           │  NDJSON over stdin/stdout
    ┌─────────▼──┐  ┌─────▼──┐  ┌────▼───┐
    │    node    │  │  node  │  │  node  │
    │ render-    │  │render- │  │render- │
    │ server.cjs │  │server  │  │server  │
    └────────────┘  └────────┘  └────────┘
```

Each worker owns exactly one Node.js subprocess. Concurrent requests to the same worker are multiplexed by assigning each a unique integer ID. A dedicated reader goroutine per worker dispatches responses back to callers by ID. Across the pool, requests are distributed by atomic round-robin. Dead workers are replaced transparently on the next request to that slot.

---

## Wire protocol

Every message in both directions is a single newline-delimited JSON object. No HTTP overhead. No length prefix. A newline is an unambiguous message boundary over a pipe.

**Request** (Go → Node):
```json
{"id":7,"component":"Counter","props":{"initial":0}}
```

**Response** (Node → Go):
```json
{"id":7,"html":"<div>...</div>","head":"<title>Counter</title>"}
```

**Error response**:
```json
{"id":7,"error":"unknown component: \"Counter\""}
```

---

## Installation

```bash
go get github.com/abiiranathan/revelt
```

Install the CLI:

```bash
go install github.com/abiiranathan/revelt/cmd/revelt@latest
```

Node.js 18+ and Go 1.24+ are required.

---

## Quick start

### 1. Initialise a project

```bash
revelt init --framework react
# or
revelt init --framework svelte --tailwind
```

Available flags:

| Flag              | Default          | Description                                         |
| ----------------- | ---------------- | --------------------------------------------------- |
| `--framework`     | `react`          | `react` or `svelte`                                 |
| `--dir`           | `.`              | Target directory                                    |
| `--source-dir`    | `frontend`       | Frontend source directory name                      |
| `--component-dir` | `src/components` | Component subdirectory (relative to `--source-dir`) |
| `--tailwind`      | `false`          | Configure Tailwind CSS v4                           |

`revelt init` writes a fully working project skeleton: `revelt.json`, a `main.go` with a ready-to-run server, a `package.json`, build tooling (`build.mjs` for React / Vite config for Svelte), a render server script, a typed registry declaration, and three example components in each mode. Run it, do `npm install`, and you have a working server-rendered app.

### 2. Install Node dependencies

```bash
cd frontend && npm install
```

### 3. Build the frontend

```bash
npm run build
```

Or start the watcher for live rebuilds during development:

```bash
npm run build:watch
```

### 4. Run the Go server

```bash
go run main.go
```

### 5. Use `revelt dev` for a full hot-reload workflow

`revelt dev` runs everything together: it starts the Node watcher for the frontend and watches `.go` files for changes, recompiling and restarting the Go server automatically on every save.

```bash
revelt dev
```

---

## Component modes

Every component file is annotated in its first five lines with a `@mode` comment. revelt reads this at build time and routes rendering accordingly.

### `@mode ssr` — server-rendered, no client JavaScript

The component is rendered to HTML by the Node sidecar. No JavaScript for this component is sent to the browser. Use it for anything static: headers, navigation, footers, data-display components that need SEO.

```tsx
// @mode ssr
interface HeaderProps { title: string; }

export default function Header({ title }: HeaderProps) {
  return (
    <header>
      <h1>{title}</h1>
    </header>
  );
}
```

### `@mode hydrate` — SSR + client hydration (default)

Rendered on the server for a fast first paint, then picked up by the browser bundle and made fully interactive. This is the default when no annotation is present.

```tsx
// @mode hydrate
import { useState } from "react";

export default function Counter({ title, initial = 0 }) {
  const [count, setCount] = useState(initial);
  return (
    <div>
      <h3>{title}: {count}</h3>
      <button onClick={() => setCount(c => c - 1)}>-</button>
      <button onClick={() => setCount(c => c + 1)}>+</button>
    </div>
  );
}
```

### `@mode client` — browser-only mount

The server emits an empty placeholder `<div>`. The client bundle mounts the component after page load. Use this for components that depend on browser APIs (`window`, `document`, WebGL, etc.) that are unavailable in Node.

```tsx
// @mode client
import { useEffect, useState } from "react";

export default function LiveChart({ label }) {
  const [data, setData] = useState([]);
  useEffect(() => setData([12, 19, 3, 5, 2, 8]), []);
  return <div>{/* chart rendering */}</div>;
}
```

### `@mode lazy-client` — deferred client-only chunk

Like `client`, but the JavaScript chunk for this component is fetched lazily the first time an island with its name appears in the DOM. The component does not contribute to the initial bundle weight. Use it for heavy components reachable only via in-app navigation.

---

## Rendering from Go

The `App` type wraps the renderer, the HTTP mux, and the HTTP server into a single object. Construct it with `NewApp`, register routes, call `Run`.

```go
package main

import (
    "context"
    "log"
    "net/http"

    "github.com/abiiranathan/revelt"
)

func main() {
    app, err := revelt.NewApp(context.Background(), "revelt.json")
    if err != nil {
        log.Fatalf("failed to start revelt: %v", err)
    }

    app.RegisterHealthEndpoints()

    app.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) error {
        if r.URL.Path != "/" {
            http.NotFound(w, r)
            return nil
        }

        return app.NewPage().
            Slot("Header", "Header", map[string]any{
                "title": "My App",
            }).
            Slot("Counter", "Counter", map[string]any{
                "title":   "Hydrated Component",
                "initial": 10,
            }).
            Slot("ClientChart", "ClientChart", map[string]any{
                "label": "Client-Only Chart",
            }).
            Render(r.Context(), w)
    })

    app.Run()
}
```

`PageBuilder.Slot` registers component renders; `Render` executes them all concurrently, merges the resulting HTML into the `index.html` template, and writes the response.

### HandlerFunc and error handling

`revelt.HandlerFunc` is `func(http.ResponseWriter, *http.Request) error`. Any non-nil return value is passed to the configured `ErrorHandler` rather than producing a duplicate `WriteHeader` call. The default handler logs the error and writes a 500. Override it with `WithErrorHandler`:

```go
app, _ := revelt.NewApp(ctx, "revelt.json",
    revelt.WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) {
        var notFound *store.NotFoundError
        if errors.As(err, &notFound) {
            http.Error(w, notFound.Error(), http.StatusNotFound)
            return
        }
        http.Error(w, http.StatusText(500), 500)
    }),
)
```

For handlers that manage their own error responses, use the standard-library-compatible registration methods:

```go
app.HandleStd("/api/health", myStdHandler)
app.HandleFuncStd("/metrics", metricsHandlerFunc)
```

### Custom templates

Serve routes with a layout different from the default `index.html`:

```go
const adminLayout = `<!DOCTYPE html><html><body>{{ .Sidebar }}{{ .Content }}</body></html>`

app.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) error {
    return app.NewPageWithTemplate(adminLayout).
        Slot("Sidebar", "AdminSidebar", nil).
        Slot("Content", "AdminDashboard", map[string]any{"user": currentUser(r)}).
        Render(r.Context(), w)
})
```

### Rendering a component directly

When you need a component's HTML fragment without a full page (for HTMX partials, JSON APIs that include markup, etc.):

```go
out, err := app.RenderComponent(r.Context(), revelt.RenderInput{
    Component: "UserCard",
    Props:     map[string]any{"id": userID},
})
if err != nil {
    return err
}
w.Write([]byte(out.HTML))
```

---

## Configuration — `revelt.json`

```json
{
  "framework":     "react",
  "source_dir":    "./frontend",
  "out_dir":       "./frontend/dist",
  "workers":       4,
  "timeout_ms":    500,
  "port":          8080,
  "static_prefix": "/static/",
  "component_dir": "src/components",
  "go_build_cmd":  "go build"
}
```

| Field           | Description                                                      |
| --------------- | ---------------------------------------------------------------- |
| `framework`     | `react` or `svelte`                                              |
| `source_dir`    | Path to the frontend source directory                            |
| `out_dir`       | Path where built bundles are written                             |
| `workers`       | Number of Node.js sidecar processes in the pool                  |
| `timeout_ms`    | Per-render timeout in milliseconds (0 = no extra timeout)        |
| `port`          | Port the Go server listens on                                    |
| `static_prefix` | URL prefix for serving the client-side bundle                    |
| `component_dir` | Component subdirectory relative to `source_dir`                  |
| `go_build_cmd`  | Go build command used by `revelt build` (defaults to `go build`) |

---

## API reference

### `revelt.NewApp`

```go
func NewApp(ctx context.Context, configPath string, opts ...AppOption) (*App, error)
```

Loads `revelt.json`, starts the Node worker pool, registers the static asset handler at `cfg.StaticPrefix`, and returns a fully initialised `App`. The supplied context is merged with an internal SIGINT/SIGTERM handler — shutdown happens on whichever fires first. Callers do not need to set up signal handling themselves.

### `(*App).HandleFunc` / `(*App).Handle`

```go
func (a *App) HandleFunc(pattern string, handler HandlerFunc)
func (a *App) Handle(pattern string, handler HandlerFunc)
```

Register a `revelt.HandlerFunc` for a URL pattern. Pattern syntax follows `http.ServeMux` (Go 1.22+ enhanced routing is supported).

### `(*App).HandleStd` / `(*App).HandleFuncStd`

```go
func (a *App) HandleStd(pattern string, handler http.Handler)
func (a *App) HandleFuncStd(pattern string, handler func(http.ResponseWriter, *http.Request))
```

Register standard-library handlers. Use for third-party middleware or handlers that manage their own error responses.

### `(*App).NewPage` / `(*App).NewPageWithTemplate`

```go
func (a *App) NewPage() *PageBuilder
func (a *App) NewPageWithTemplate(tmpl string) *PageBuilder
```

Return a `PageBuilder`. `NewPage` uses `dist/client/index.html` as the template. `NewPageWithTemplate` accepts a raw HTML string for per-route layouts.

### `(*PageBuilder).Slot`

```go
func (b *PageBuilder) Slot(key, component string, props map[string]any) *PageBuilder
```

Registers a component render to be injected at `{{ .Key }}` in the template. Chainable. All slots render concurrently when `Render` is called.

### `(*PageBuilder).Render`

```go
func (b *PageBuilder) Render(ctx context.Context, w http.ResponseWriter) error
```

Executes all registered slots concurrently, merges results into the template, and writes the full HTML response. Sets `Content-Type: text/html; charset=utf-8` and a `no-cache` directive on `index.html`.

### `(*App).RenderComponent`

```go
func (a *App) RenderComponent(ctx context.Context, in RenderInput) (RenderOutput, error)
```

Renders a single component and returns its HTML and head fragments directly, without a page template. Safe for concurrent use.

### `(*App).RegisterHealthEndpoints`

```go
func (a *App) RegisterHealthEndpoints()
```

Mounts `/healthz` (liveness) and `/readyz` (readiness) on the app's mux. Not registered by default.

### `(*App).Run`

```go
func (a *App) Run()
```

Starts the HTTP server, blocks until the app's context is cancelled, then performs graceful shutdown: closes the renderer (draining in-flight SSR calls), waits up to `shutdownTimeout` for active HTTP connections to finish, then returns.

### `revelt.New` (lower-level)

```go
func New(ctx context.Context, script string, opts ...Option) (*Renderer, error)
```

Constructs a bare `Renderer` without the HTTP server. Use this when you need to integrate revelt's SSR pool into an existing HTTP setup.

### `(*Renderer).Render`

```go
func (r *Renderer) Render(ctx context.Context, in RenderInput) (RenderOutput, error)
```

Renders a single component. Blocks until the Node process responds or the context is cancelled. Safe for concurrent use by multiple goroutines.

### `(*Renderer).Stats`

```go
func (r *Renderer) Stats() []WorkerStat
```

Returns a liveness snapshot of every worker in the pool.

### `(*Renderer).Close`

```go
func (r *Renderer) Close() error
```

Closes stdin for every worker (triggering a clean EOF-based exit in Node), then waits for all processes to terminate.

### Renderer options

| Option                   | Default              | Description                                                  |
| ------------------------ | -------------------- | ------------------------------------------------------------ |
| `WithWorkers(n)`         | `runtime.NumCPU()`   | Number of Node processes in the pool                         |
| `WithNodeBin(path)`      | `"node"`             | Node.js binary path (useful with nvm or asdf)                |
| `WithRenderTimeout(d)`   | 0 (no extra timeout) | Per-request deadline applied on top of any context deadline  |
| `WithReadBufSize(n)`     | 64 KiB               | `bufio.Reader` buffer size per worker stdout pipe            |
| `WithBuildCmd(args...)`  | nil                  | Command to compile JSX/TSX/Svelte before starting workers    |
| `WithBuildDir(dir)`      | `"."`                | Working directory for `WithBuildCmd`                         |
| `WithProjectConfig(cfg)` | nil                  | Attaches a loaded `ProjectConfig` to skip unknown components |

### App options

| Option                   | Default       | Description                                           |
| ------------------------ | ------------- | ----------------------------------------------------- |
| `WithReadTimeout(d)`     | 5s            | HTTP server read timeout                              |
| `WithWriteTimeout(d)`    | 30s           | HTTP server write timeout                             |
| `WithIdleTimeout(d)`     | 120s          | HTTP server keep-alive idle timeout                   |
| `WithShutdownTimeout(d)` | 10s           | Graceful drain window before forced close             |
| `WithMiddleware(mw...)`  | none          | Handler wrappers applied outermost-first              |
| `WithLogger(l)`          | `log.Default` | Custom logger                                         |
| `WithErrorHandler(fn)`   | logs + 500    | Handler for errors returned from `HandlerFunc` routes |
| `WithoutCompression()`   | gzip enabled  | Disable on-the-fly gzip for static assets             |

---

## Health endpoints

```go
app.RegisterHealthEndpoints()
// or, on a custom mux using the health sub-package:
import "github.com/abiiranathan/revelt/health"
mux.Handle("/healthz", health.Liveness(renderer))
mux.Handle("/readyz",  health.Readiness(renderer))
```

`/healthz` — always `200 OK` while the Go process is alive. Intended for Kubernetes liveness probes.

`/readyz` — `200 OK` when at least one worker is alive; `503 Service Unavailable` when the entire pool is dead. Kubernetes readiness probes use this to pull the pod from rotation.

`/readyz` response body:

```json
{
  "status": "ok",
  "alive_workers": 4,
  "total_workers": 4,
  "workers": [
    { "index": 0, "alive": true, "stderr": "" },
    { "index": 1, "alive": true, "stderr": "" }
  ]
}
```

---

## CLI reference

### `revelt init`

Scaffolds a complete project: Go server, revelt config, frontend build tooling, and example components.

```bash
revelt init [flags]

Flags:
  --framework     react or svelte (default: react)
  --dir           target directory (default: .)
  --source-dir    frontend source directory name (default: frontend)
  --component-dir component subdirectory (default: src/components)
  --tailwind      configure Tailwind CSS v4
```

### `revelt build`

Runs the frontend build (`node build.mjs` in `source_dir`) and the Go build (`go_build_cmd` from `revelt.json`) concurrently. Both must succeed for the command to exit zero.

```bash
revelt build
```

### `revelt dev`

Starts the Node file watcher and the Go server together. Watches `.go` files and `revelt.json` for changes; recompiles and restarts the Go binary automatically. The Node watcher handles frontend rebuilds.

```bash
revelt dev
```

### `revelt update`

Rewrites the framework-owned files (`build.mjs`, `render-server.js`, `client.tsx`/`client.ts`, `revelt.types.d.ts`) from the current templates, preserving your `revelt.json` settings. Use this when upgrading revelt to pick up build tooling improvements without reinitialising the project.

```bash
revelt update [--dry-run]

Flags:
  --dry-run   print files that would be updated without writing them
```

### `revelt version`

```bash
revelt version
```

---

## Client-side routing

Out of the box, client-side navigation works via the built-in History API router in `client.tsx`/`client.ts`. Anchor clicks to same-origin paths are intercepted, the new page HTML is fetched from Go, the body is replaced, and islands are re-hydrated.

If the user refreshes on a path served by the client router (e.g. `/dashboard`), the request reaches Go. Without a catch-all, Go returns 404. The fix is a catch-all handler that returns the index shell for any unknown path:

```go
app.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) error {
    // Known API routes are registered before this handler and shadow it.
    // Only paths not matched elsewhere reach here.
    return app.NewPage().
        Slot("Header", "Header", map[string]any{"title": "My App"}).
        Render(r.Context(), w)
})
```

Go's `ServeMux` matches the most specific prefix first, so dedicated routes (`/api/`, `/static/`) continue to work:

```go
app.HandleStd("/api/", apiRouter)   // handled before the catch-all
app.HandleStd(cfg.StaticPrefix, ...) // registered automatically by NewApp
app.HandleFunc("/", shellHandler)    // catch-all for SPA routes
```

For per-route SSR (different server-rendered content per URL), read the path in the handler and pass it as props:

```go
app.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) error {
    return app.NewPage().
        Slot("App", "AppShell", map[string]any{
            "initialRoute": r.URL.Path,
            "initialData":  fetchDataForRoute(r.Context(), r.URL.Path),
        }).
        Render(r.Context(), w)
})
```

---

## Static asset caching

The static handler (mounted automatically at `cfg.StaticPrefix`) applies:

- `Cache-Control: public, max-age=31536000, immutable` for content-hashed chunks under `chunks/`.
- `Cache-Control: no-cache` for the entry file and CSS (so updated hashes are always fetched).
- On-the-fly gzip compression for clients that send `Accept-Encoding: gzip` (disable with `WithoutCompression()`).

`index.html` itself is served with `Cache-Control: no-cache` so browsers always revalidate before serving a cached copy, while still benefiting from ETag-based conditional requests.

---

## Deployment

A revelt application deploys as a single image: the Go binary plus a Node runtime. No separate Node HTTP server, no `npm install` in production, no sidecar service to register.

```dockerfile
# Stage 1: build the frontend
FROM node:20-alpine AS frontend
WORKDIR /app/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN node build.mjs

# Stage 2: build the Go binary
FROM golang:1.24-alpine AS gobuilder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
# The compiled frontend must exist before `go build` so go:embed resolves.
COPY --from=frontend /app/frontend/dist ./frontend/dist
COPY . .
RUN CGO_ENABLED=0 go build -o /out/server ./...

# Stage 3: minimal runtime image
FROM node:20-alpine
WORKDIR /app
COPY --from=gobuilder /out/server        ./server
COPY --from=frontend  /app/frontend/dist ./frontend/dist
COPY revelt.json                          ./
EXPOSE 8080
CMD ["./server"]
```

`render-server.cjs` is a self-contained CommonJS bundle with no `node_modules` of its own — esbuild (React) or Vite (Svelte) bundled everything into it.

For Kubernetes, use `RegisterHealthEndpoints()` and configure probes against `/healthz` (liveness) and `/readyz` (readiness). Horizontal scaling is straightforward: each replica owns its own sidecar pool, there is no shared state between instances.

Read the full deployment guide at [docs/deployment.md](./docs/deployment.md).

---

## Scaling

Each Node process is single-threaded. Set `workers` in `revelt.json` to match the available CPU count (the default when using `revelt.New` directly is `runtime.NumCPU()`). For horizontal scaling, add replicas behind a load balancer — each pod is fully independent.

For components that produce HTML larger than 64 KiB, increase `WithReadBufSize` accordingly.

---

## Requirements

- Go 1.24+
- Node.js 18+

---

## License

MIT
