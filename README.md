# Revelte

> A production-ready Go library for **server-side rendering of Svelte and React components** via a pool of supervised Node.js sidecar processes.

Drop React or Svelte components into any Go web app — server-rendered, hydrated, and production-ready out of the box.

Revelte is a Go-native islands architecture framework that lets you render React or Svelte components directly from your Go HTTP handlers. Components are server-rendered via a pool of supervised Node.js sidecar processes, selectively hydrated on the client, and served through your existing Go server — no separate Node server, no framework lock-in.

---

## Architecture
```
┌─────────────────────────────────────────────────────────┐
│                   Go HTTP Server                        │
│                                                         │
│   renderer.Render(ctx, RenderInput{...})                │
│           │                                             │
│     ┌─────▼──────────────────────────────────┐          │
│     │          Pool (round-robin)            │          │
│     │  ┌────────┐ ┌────────┐ ┌────────┐      │          │
│     │  │worker 0│ │worker 1│ │worker 2│  ... │          │
│     │  └───┬────┘ └───┬────┘ └───┬────┘      │          │
│     └───────┼─────────┼──────────┼───────────┘          │
└─────────────┼─────────┼──────────┼──────────────────────┘
              │         │          │   NDJSON over stdin/stdout
    ┌─────────▼──┐ ┌─────▼──┐ ┌────▼───┐
    │  node      │ │  node  │ │  node  │
    │ render-    │ │render- │ │render- │
    │ server.cjs │ │server  │ │server  │
    └────────────┘ └────────┘ └────────┘
```

Each worker owns exactly one Node.js subprocess. Within a single worker, **concurrent requests are multiplexed** by assigning each request a unique integer ID. A dedicated reader goroutine per worker reads responses from stdout and dispatches them to the correct waiting goroutine by ID.

Across the pool, requests are distributed by **atomic round-robin**. Dead workers are detected on each dispatch and transparently replaced before the request is forwarded.

---

## Wire protocol

Every message in both directions is a **newline-delimited JSON** (NDJSON) object — one JSON object, one `\n`. No length prefix. No HTTP overhead. A newline is an unambiguous message boundary on a pipe.

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
go get github.com/abiiranathan/revelte
```

Install the CLI:

```bash
go install github.com/abiiranathan/revelte/cmd/revelte@latest
```

---

## Quick start

### 1. Initialise a project

```bash
revelte init --framework react
```

Available flags:

```
--framework     react or svelte (default: react)
--dir           target directory (default: .)
--source-dir    frontend source directory name (default: frontend)
--component-dir component subdirectory name (default: components)
```

### 2. Install Node dependencies

```bash
cd frontend && npm install
```

### 3. Add your components

Drop any `.tsx`, `.ts`, `.jsx`, or `.js` file (React) or `.svelte` file (Svelte)
into the `components/` directory — or whatever `component_dir` is set to in
`revelte.json`. Revelte discovers them automatically at build time. No
registration required.

Annotate each file with a `@mode` comment in the first five lines to control
how it is rendered:

```
// @mode ssr       → server-rendered only, zero client JS
// @mode hydrate   → server-rendered + hydrated on the client (default)
// @mode client    → skips SSR entirely, mounted in the browser only
```

### 4. Build

```bash
npm run build          # produces dist/render-server.cjs + dist/client/client.js
npm run build:watch    # rebuild on every file save (development)
```

### 5. Run

```bash
cd .. && go run main.go
```

---

## Component modes

### `ssr` — server-rendered only

The component is rendered to HTML on the server. No JavaScript is sent to the
browser for this component. Use this for static markup: headers, footers,
navigation, anything with no interactivity.

```tsx
// @mode ssr
interface HeaderProps {
  title: string;
}

export default function Header({ title }: HeaderProps) {
  return (
    <header>
      <h1>{title}</h1>
    </header>
  );
}
```

### `hydrate` — server-rendered and hydrated (default)

The component is server-rendered for fast initial paint, then hydrated on the
client so it becomes fully interactive. This is the default when no `@mode`
annotation is present.

```tsx
// @mode hydrate
import { useState } from "react";

interface CounterProps {
  title: string;
  initial?: number;
}

export default function Counter({ title, initial = 0 }: CounterProps) {
  const [count, setCount] = useState(initial);
  return (
    <div>
      <h3>{title}</h3>
      <button onClick={() => setCount(c => c - 1)}>-</button>
      <span>{count}</span>
      <button onClick={() => setCount(c => c + 1)}>+</button>
    </div>
  );
}
```

### `client` — browser-only

The component is skipped during SSR entirely. The server emits an empty
placeholder; the client bundle mounts the component in the browser. Use this
for components that depend on browser APIs unavailable in Node.js.

```tsx
// @mode client
import { useEffect, useState } from "react";

interface ClientChartProps {
  label: string;
}

export default function ClientChart({ label }: ClientChartProps) {
  const [data, setData] = useState<number[]>([]);
  useEffect(() => { setData([12, 19, 3, 5, 2, 3]); }, []);
  return (
    <div>
      <h3>{label}</h3>
      {data.map((v, i) => <span key={i}>{v}</span>)}
    </div>
  );
}
```

---

## Rendering from Go

```go
package main

import (
    "bytes"
    "html/template"
    "net/http"

    "github.com/abiiranathan/revelte/revelte"
)

func handler(renderer *revelte.Renderer, indexPage string) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        header, err := renderer.Render(r.Context(), revelte.RenderInput{
            Component: "Header",
            Props:     map[string]any{"title": "My App"},
        })
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }

        counter, err := renderer.Render(r.Context(), revelte.RenderInput{
            Component: "Counter",
            Props:     map[string]any{"title": "Clicks", "initial": 0},
        })
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }

        t, _ := template.New("index").Parse(indexPage)
        buf := new(bytes.Buffer)
        t.Execute(buf, map[string]any{
            "Header":  template.HTML(header.HTML),
            "Counter": template.HTML(counter.HTML),
        })

        w.Header().Set("Content-Type", "text/html; charset=utf-8")
        w.Write(buf.Bytes())
    }
}
```
> Remember, you can modify the index.html to your liking. You can build a common layout for your apps and render it as required.

---

## Configuration — `revelte.json`

```json
{
  "framework":     "react",
  "source_dir":    "./frontend",
  "out_dir":       "./frontend/dist",
  "workers":       4,
  "timeout_ms":    500,
  "port":          8080,
  "static_prefix": "/static/",
  "component_dir": "components"
}
```

| Field           | Description                                     |
| --------------- | ----------------------------------------------- |
| `framework`     | `react` or `svelte`                             |
| `source_dir`    | Path to the frontend source directory           |
| `out_dir`       | Path where built bundles are written            |
| `workers`       | Number of Node.js sidecar processes in the pool |
| `timeout_ms`    | Per-render timeout in milliseconds              |
| `port`          | Port the Go server listens on                   |
| `static_prefix` | URL prefix for serving client-side assets       |
| `component_dir` | Component subdirectory relative to `source_dir` |

---

## API reference

### `revelte.New`

```go
func New(ctx context.Context, script string, opts ...Option) (*Renderer, error)
```

Starts a pool of Node.js workers and returns a `Renderer`. The context controls
worker process lifetimes — cancelling it sends SIGKILL to every worker.

If `WithBuildCmd` is configured, the build runs synchronously before workers are
started. A non-zero exit code returns an error including the full build output.

### `(*Renderer).Render`

```go
func (r *Renderer) Render(ctx context.Context, in RenderInput) (RenderOutput, error)
```

Renders a single component. Blocks until the Node process responds or the
context is cancelled. Safe for concurrent use by multiple goroutines.

### `(*Renderer).Stats`

```go
func (r *Renderer) Stats() []WorkerStat
```

Returns a liveness snapshot of every worker. Use this to build `/healthz`
endpoints or feed metrics exporters.

### `(*Renderer).Close`

```go
func (r *Renderer) Close() error
```

Closes stdin for every worker (triggering a clean exit) then waits for all
processes to terminate. Safe to call while renders are in flight.

### Options

| Option                  | Default              | Description                                               |
| ----------------------- | -------------------- | --------------------------------------------------------- |
| `WithWorkers(n)`        | `runtime.NumCPU()`   | Number of Node processes in the pool                      |
| `WithNodeBin(path)`     | `"node"`             | Node.js binary path (useful with nvm or asdf)             |
| `WithRenderTimeout(d)`  | 0 (no extra timeout) | Per-request deadline on top of any context deadline       |
| `WithReadBufSize(n)`    | 64 KiB               | `bufio.Reader` buffer size per worker stdout              |
| `WithBuildCmd(args...)` | nil (disabled)       | Command to compile JSX/TSX/Svelte before starting workers |
| `WithBuildDir(dir)`     | `"."`                | Working directory for `WithBuildCmd`                      |

---

## Health endpoints

```go
import "github.com/abiiranathan/revelte/health"

mux.Handle("/healthz", health.Liveness(renderer))  // 200 if the Go process is alive
mux.Handle("/readyz",  health.Readiness(renderer)) // 503 if all workers are dead
```

`/readyz` response body:

```json
{
  "status": "ok",
  "alive_workers": 4,
  "total_workers": 4,
  "workers": [
    { "index": 0, "alive": true, "stderr": "" }
  ]
}
```

---

## CLI reference

```
revelte init    Initialise a new Revelte project
  --framework     react or svelte (default: react)
  --dir           target directory (default: .)
  --source-dir    frontend source directory name (default: frontend)
  --component-dir component subdirectory name (default: components)

revelte build   Build server and client bundles
revelte dev     Start the file watcher and Go server together
```

---

## Scaling

- **Single host** — set `WithWorkers(runtime.NumCPU())` (the default). Each
  worker is single-threaded JavaScript, so one process per CPU core is optimal.
- **Multiple hosts** — run one Go server per machine. The sidecar pattern scales
  horizontally through your existing load balancer.
- **Large payloads** — increase `WithReadBufSize` if individual renders produce
  HTML larger than 64 KiB.

---

## Node process management

Workers are not restarted proactively. A dead worker slot is replaced
transparently on the next request that targets it. For long-running servers,
consider a background goroutine that calls `Stats()` periodically and
re-creates the renderer if all workers are found dead.

Context cancellation (e.g. on server shutdown) sends SIGKILL via
`exec.CommandContext`. If you need a graceful drain — letting in-flight renders
finish before shutdown — cancel the context only after all outstanding `Render`
calls have returned.

---

## Requirements

- Go 1.24+
- Node.js 18+

---

## License

MIT
