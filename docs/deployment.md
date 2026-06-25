# Deploying a revelt Application

A revelt application is a standard Go binary that happens to fork one or more
Node.js subprocesses at startup. Deployment is therefore simpler than a
split-stack architecture (separate Node server + Go API) — you ship one process
that owns everything — but the sidecar dependency introduces a few constraints
worth understanding before you write your `Dockerfile`.

---

## What you are actually shipping

```
your-server (Go binary)
  └─ spawns N × node render-server.cjs  (sidecar pool)
```

The Node processes are not separate network services. They are child processes
sharing the Go binary's process group, communicating over anonymous pipes
(`stdin`/`stdout`). They live and die with the parent. From the OS and
container runtime's perspective there is one entry point.

The build artefacts that must be present at runtime are:

| Artefact                 | Where                                     | Purpose                                                 |
| ------------------------ | ----------------------------------------- | ------------------------------------------------------- |
| `your-server`            | anywhere on `PATH`                        | Go HTTP server                                          |
| `render-server.cjs`      | `cfg.OutDir` (default `./frontend/dist/`) | Node SSR sidecar                                        |
| `dist/client/client.js`  | `cfg.OutDir/client/`                      | Browser hydration bundle                                |
| `dist/client/index.html` | same                                      | HTML template (embedded at compile time via `go:embed`) |
| `revelt.json`            | working directory                         | Runtime config (workers, port, timeouts)                |
| `node` binary            | `PATH` or `WithNodeBin` path              | Node.js runtime                                         |

---

## Dockerfile

```dockerfile
# ── Stage 1: build the frontend ──────────────────────────────────────────────
FROM node:20-alpine AS frontend
WORKDIR /app/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN node build.mjs

# ── Stage 2: build the Go binary ─────────────────────────────────────────────
FROM golang:1.24-alpine AS gobuilder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
# The frontend dist must exist before `go build` so go:embed resolves.
COPY --from=frontend /app/frontend/dist ./frontend/dist
COPY . .
RUN CGO_ENABLED=0 go build -o /out/server ./...

# ── Stage 3: minimal runtime image ───────────────────────────────────────────
FROM node:20-alpine
WORKDIR /app

# Only the artefacts the running server needs.
COPY --from=gobuilder /out/server        ./server
COPY --from=frontend  /app/frontend/dist ./frontend/dist
COPY revelt.json                          ./

EXPOSE 8080
CMD ["./server"]
```

Key points:

- The `go:embed` directive in `main.go` (`//go:embed frontend/dist/client/index.html`) means `dist/client/index.html` **must exist when `go build` runs**, not just at runtime. Stage 2 copies it from Stage 1 before compiling.
- Node.js must be present in the final image. Using `node:20-alpine` as the base gives you both Node and a small footprint.
- `render-server.cjs` is a CommonJS bundle that has no `node_modules` of its own (esbuild bundled everything into it). No `npm install` in the runtime stage.

---

## Process supervision

The sidecar pool is supervised by the Go process, not by your container
orchestrator. Worker crash → automatic replacement on next request (see
`pool.workerAt`). Container crash → your orchestrator restarts the whole pod.

For multi-worker deployments, set `workers` in `revelt.json` to match the
available CPU count. The Node processes are single-threaded; one per core is the
right model.

```json
{
  "workers": 4
}
```

If you want dynamic sizing, override it at startup:

```go
revelt.WithWorkers(runtime.NumCPU())
```

---

## Health probes (Kubernetes)

The `health` package gives you the two probes Kubernetes expects.

```yaml
livenessProbe:
  httpGet:
    path: /healthz
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 10

readinessProbe:
  httpGet:
    path: /readyz
    port: 8080
  initialDelaySeconds: 2
  periodSeconds: 5
```

`/readyz` returns `503` when all workers are dead, which removes the pod from
rotation. `/healthz` always returns `200` while the Go process is alive — it is
not a worker health check and should not be used as one.

---

## Graceful shutdown

The `main.go` template already does this correctly:

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()
// ... start server ...
<-ctx.Done()
stop()
shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
srv.Shutdown(shutdownCtx)
```

Context cancellation propagates to `exec.CommandContext`, which sends SIGKILL to
the Node children. If you need the Node processes to flush in-flight renders
before dying, call `renderer.Close()` (which closes stdin and waits for `rl.on('close')`) *before* cancelling the context, not after.

---

## Scaling

Because every Go instance owns its own sidecar pool, horizontal scaling is a
matter of adding more replicas behind a load balancer. There is no shared state
between instances; each serves requests fully independently.

```
Load Balancer
  ├─ pod-0  (Go + 4× Node workers)
  ├─ pod-1  (Go + 4× Node workers)
  └─ pod-2  (Go + 4× Node workers)
```

This scales well. The Node processes are the throughput ceiling; more pods = more
Node processes = more SSR capacity.

---

## Client-side routing: the constraint and the fix

### The constraint

Yes — out of the box, client-side routing does not work with this architecture.

Here is why. Your Go mux currently has two route registrations:

```go
mux.Handle(cfg.StaticPrefix, http.StripPrefix(...))  // /static/*
mux.HandleFunc("/", ...)                              // everything else → renders index
```

The `"/"` handler returns a full HTML page for the index route. When the
browser's JavaScript router navigates to `/dashboard` or `/users/42`, it
rewrites the URL using the History API without a network request — that works
fine. But if the user **refreshes** on `/dashboard`, the browser sends `GET
/dashboard` to Go. Go has no route for that, returns a 404, and the app breaks.

This is the classical SPA deployment problem, not specific to revelt. It is
solved the same way regardless of whether you use React Router, TanStack Router,
or SvelteKit's client router.

### Fix 1: Go catch-all (simplest, zero dependencies)

Serve the index HTML for any path that does not match a known API route or static
asset. The JavaScript router takes over once the bundle loads.

```go
mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
    // Exact-path API routes are registered before this handler, so they
    // shadow it. Only unknown paths (including /dashboard, /users/42) reach here.
    serveIndex(w, r, renderer, cfg)
})
```

Because Go's `ServeMux` matches the most specific prefix first, you can keep
registering specific routes without them being swallowed by the catch-all:

```go
mux.Handle("/api/", apiHandler)          // handled by Go
mux.Handle("/static/", staticHandler)    // handled by Go
mux.HandleFunc("/", serveIndex)          // everything else → SPA shell
```

`serveIndex` server-renders whichever components make up the shell (header,
layout, etc.) and emits the hydration bundle. The JS router then reads
`window.location.pathname` and mounts the correct view.

### Fix 2: Route-aware SSR (islands per route)

If you want each route to arrive with its own server-rendered content — not just
a shell — you match the URL in Go and pass the route context to the renderer as
props.

```go
mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
    route := r.URL.Path  // e.g. "/dashboard", "/users/42"

    // Server-render the layout shell, which contains the router's initial state.
    shell, err := renderer.Render(r.Context(), revelt.RenderInput{
        Component: "AppShell",
        Props: map[string]any{
            "initialRoute": route,
            "initialData":  fetchDataForRoute(r.Context(), route),
        },
    })
    // ... write HTML ...
})
```

The React/Svelte router is initialised with `initialRoute` on the server, so it
renders the correct view for that URL. After hydration the browser router takes
over for subsequent navigations. This is the islands + SSR model that
Next.js/SvelteKit implement internally.

### Fix 3: Hash-based routing (avoid if possible)

Routes like `/#/dashboard` never reach the server; the browser never sends the
fragment. No server changes needed. Avoid this for new projects — it breaks
semantic URLs, `<meta og:url>`, and any anchor-based deep linking.

### Which fix to use

| Situation                                                   | Recommendation                                        |
| ----------------------------------------------------------- | ----------------------------------------------------- |
| Marketing site, mostly static content                       | Fix 1 (catch-all). Simple, no component changes.      |
| App with real routes that need per-page SEO or initial data | Fix 2 (route-aware SSR). More work but better output. |
| Internal tool, SEO irrelevant                               | Either Fix 1 or Fix 3 (hash routing).                 |

For most revelt apps Fix 1 is the right starting point. Add Fix 2 per-route as
SEO or perceived-performance requirements demand it.

---

## Static asset caching

The esbuild client bundle currently outputs `client.js` with a fixed filename.
For production, enable content hashing so you can set long `Cache-Control` headers:

In `build.mjs`, change the client output options:

```js
const clientBuildOptions = {
    // ...
    entryNames: '[name]-[hash]',  // → client-ABCD1234.js
};
```

`injectAssets()` already reads `dist/client/` and injects whatever `.js` and
`.css` files it finds, so the hash is picked up automatically. Then serve static
assets with:

```go
// In your Go handler
w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
```

and set a short `Cache-Control` on `index.html` itself (`no-cache` or
`max-age=0, must-revalidate`) so browsers always fetch the latest HTML with the
updated script src.

---

## Summary

- Ship a single Docker image: Go binary + Node runtime + `render-server.cjs` + client bundle.
- No separate Node HTTP server needed; the sidecar is pipe-connected, not network-connected.
- Client-side routing works — you just need a Go catch-all that returns the index HTML for any path not claimed by an API or static route.
- For per-route SSR, pass `r.URL.Path` and pre-fetched data as props to a router-aware shell component.
- 