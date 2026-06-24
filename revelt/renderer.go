// Package revelt provides a framework for server-side rendering of Svelte
// and React components via a pool of supervised Node.js sidecar processes.
package revelt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/abiiranathan/revelt/protocol"
)

// RenderInput describes a single server-side render request.
type RenderInput struct {
	// Component is the registered identifier name of the component.
	Component string

	// Props contains the data parameters forwarded to the component.
	Props map[string]any
}

// RenderOutput holds the compiled markup and head tags after execution.
type RenderOutput struct {
	// HTML is the serialized component markup.
	HTML string

	// Head contains any page title, meta, or link tag injections.
	Head string
}

// WorkerStat describes the liveness state of an individual Node worker process.
type WorkerStat struct {
	// Index is the zero-based positioning of the worker inside the process ring.
	Index int

	// Alive tracks whether the underlying subprocess loop is running.
	Alive bool

	// Stderr contains captured runtime error logs from the process.
	Stderr string
}

// Config maps tunable parameters for initializing a Renderer.
type Config struct {
	// Workers sets the subprocess pool size.
	Workers int

	// NodeBin points to the target node executable binary.
	NodeBin string

	// RenderTimeout sets an optional hard limit on component rendering executions.
	RenderTimeout time.Duration

	// ReadBufSize controls the I/O buffer assigned to process stdout pipes.
	ReadBufSize int

	// BuildCmd contains arguments to compile frontend assets before initialization.
	BuildCmd []string

	// BuildDir specifies the execution directory for BuildCmd.
	BuildDir string

	// ProjectCfg integrates loaded revelt.json options into the renderer.
	ProjectCfg *ProjectConfig
}

// Option modifies a Config during initialization.
type Option func(*Config)

// WithWorkers overrides the subprocess pool size.
func WithWorkers(n int) Option {
	return func(c *Config) {
		if n < 1 {
			panic("revelt: WithWorkers: n must be >= 1")
		}
		c.Workers = n
	}
}

// WithNodeBin overrides the Node.js executable path.
func WithNodeBin(bin string) Option {
	return func(c *Config) { c.NodeBin = bin }
}

// WithRenderTimeout sets an optional rendering timeout.
func WithRenderTimeout(d time.Duration) Option {
	return func(c *Config) { c.RenderTimeout = d }
}

// WithReadBufSize overrides the standard stdout read buffer size.
func WithReadBufSize(n int) Option {
	return func(c *Config) { c.ReadBufSize = n }
}

// WithBuildCmd registers a build execution pipeline to compile front-end files.
func WithBuildCmd(args ...string) Option {
	return func(c *Config) {
		if len(args) == 0 {
			panic("revelt: WithBuildCmd: at least one argument required")
		}
		c.BuildCmd = args
	}
}

// WithBuildDir configures the execution directory for the compilation pipeline.
func WithBuildDir(dir string) Option {
	return func(c *Config) { c.BuildDir = dir }
}

// WithProjectConfig assigns structured configuration parameters to the renderer.
func WithProjectConfig(projectCfg *ProjectConfig) Option {
	return func(c *Config) {
		c.ProjectCfg = projectCfg
	}
}

// defaultConfig returns an initialization configuration populated with defaults.
func defaultConfig() Config {
	return Config{
		Workers:       runtime.NumCPU(),
		NodeBin:       "node",
		RenderTimeout: 0,
		ReadBufSize:   64 * 1024,
		BuildDir:      ".",
	}
}

// Renderer manages process pooling and executes server-side component rendering.
type Renderer struct {
	pool       *pool
	cfg        Config
	projectCfg *ProjectConfig
}

// New constructs and runs a Renderer backed by a supervised process pool.
func New(ctx context.Context, script string, opts ...Option) (*Renderer, error) {
	_, err := os.Stat(script)
	if err != nil {
		return nil, fmt.Errorf("stat failed for script: %v", err)
	}
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}

	if len(cfg.BuildCmd) > 0 {
		if err := runBuild(ctx, cfg.BuildCmd, cfg.BuildDir); err != nil {
			return nil, fmt.Errorf("revelt.New: build step failed: %w", err)
		}
	}

	wCfg := workerConfig{
		NodeBin:     cfg.NodeBin,
		ReadBufSize: cfg.ReadBufSize,
	}

	p, err := newPool(ctx, script, cfg.Workers, wCfg)
	if err != nil {
		return nil, fmt.Errorf("revelt.New: %w", err)
	}

	return &Renderer{
		pool:       p,
		cfg:        cfg,
		projectCfg: cfg.ProjectCfg,
	}, nil
}

// runBuild runs synchronous frontend compilation processes before process pool startup.
func runBuild(ctx context.Context, args []string, dir string) error {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("command %q in %q: %w\n%s", args, dir, err, out.String())
	}
	return nil
}

// Render processes an execution request. If the component's execution mode is
// "client", server-side execution is bypassed and Go generates the empty mount
// container locally without a Node round-trip.
func (r *Renderer) Render(ctx context.Context, in RenderInput) (RenderOutput, error) {
	if in.Component == "" {
		return RenderOutput{}, fmt.Errorf("revelt: Component must not be empty")
	}

	if r.projectCfg != nil {
		mode, known := r.projectCfg.ComponentModes[in.Component]
		if !known {
			return RenderOutput{}, fmt.Errorf("revelt: unknown component %q", in.Component)
		}

		if mode == ModeClient {
			var propsJSON []byte
			if in.Props != nil {
				var err error
				propsJSON, err = json.Marshal(in.Props)
				if err != nil {
					return RenderOutput{}, fmt.Errorf("revelt: marshalling client-only props: %w", err)
				}
			} else {
				propsJSON = []byte("{}")
			}

			escapedProps := escapeHTML(string(propsJSON))
			placeholder := fmt.Sprintf(
				`<div data-ssr-island="%s" data-ssr-props="%s"></div>`,
				in.Component, escapedProps,
			)
			return RenderOutput{HTML: placeholder, Head: ""}, nil
		}
	}

	if r.cfg.RenderTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.cfg.RenderTimeout)
		defer cancel()
	}

	req := protocol.RenderRequest{
		Component: in.Component,
		Props:     in.Props,
	}

	resp, err := r.pool.render(ctx, req)
	if err != nil {
		return RenderOutput{}, fmt.Errorf("revelt: render failed: %w", err)
	}
	if resp.Error != "" {
		return RenderOutput{}, fmt.Errorf("revelt: component %q: %s", in.Component, resp.Error)
	}

	return RenderOutput{HTML: resp.HTML, Head: resp.Head}, nil
}

// Stats returns a point-in-time state snapshot of the active worker pool.
func (r *Renderer) Stats() []WorkerStat {
	return r.pool.stats()
}

// Close closes process pipes to trigger clean shutdown of supervised workers.
func (r *Renderer) Close() error {
	if err := r.pool.close(); err != nil {
		return fmt.Errorf("revelt: closing pool: %w", err)
	}
	return nil
}

// escapeHTML encodes JSON payloads embedded directly into HTML attribute values.
func escapeHTML(text string) string {
	var buf bytes.Buffer
	for _, r := range text {
		switch r {
		case '&':
			buf.WriteString("&amp;")
		case '<':
			buf.WriteString("&lt;")
		case '>':
			buf.WriteString("&gt;")
		case '"':
			buf.WriteString("&quot;")
		case '\'':
			buf.WriteString("&#039;")
		default:
			buf.WriteRune(r)
		}
	}
	return buf.String()
}
