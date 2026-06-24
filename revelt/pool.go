package revelt

import (
	"context"     // for context.Context
	"fmt"         // for fmt.Errorf
	"sync"        // for sync.RWMutex
	"sync/atomic" // for atomic.Uint64

	"github.com/abiiranathan/revelt/protocol"
)

// pool manages a fixed-size ring of workers. Each worker owns one Node.js
// subprocess. Requests are distributed across the ring using atomic round-robin
// so no single worker becomes a bottleneck.
//
// When a worker is found to be dead (its process crashed), the pool
// transparently replaces it before the next request reaches that slot.
//
// The zero value is not usable; construct via newPool.
type pool struct {
	workers []*worker
	mu      sync.RWMutex // guards replacement of individual worker slots

	script string
	cfg    workerConfig
	ctx    context.Context // lifetime context for all spawned workers

	// cursor is atomically incremented for round-robin selection.
	cursor atomic.Uint64

	// size is len(workers), kept separately to avoid a lock on the hot path.
	size uint64
}

// newPool starts size Node.js workers all executing script and returns a pool
// ready to accept requests.
func newPool(ctx context.Context, script string, size int, cfg workerConfig) (*pool, error) {
	if size <= 0 {
		return nil, fmt.Errorf("revelt pool: size must be > 0, got %d", size)
	}

	p := &pool{
		workers: make([]*worker, size),
		script:  script,
		cfg:     cfg,
		ctx:     ctx,
		size:    uint64(size),
	}

	for i := range size {
		w, err := newWorker(ctx, script, cfg)
		if err != nil {
			// Shut down any already-started workers before returning.
			_ = p.closeRange(0, i)
			return nil, fmt.Errorf("revelt pool: starting worker %d: %w", i, err)
		}
		p.workers[i] = w
	}

	return p, nil
}

// render picks a healthy worker in round-robin order and delegates the request.
// If the selected worker is dead it is replaced transparently before the call.
func (p *pool) render(ctx context.Context, req protocol.RenderRequest) (protocol.RenderResponse, error) {
	idx := int(p.cursor.Add(1) % p.size)

	w, err := p.workerAt(idx)
	if err != nil {
		return protocol.RenderResponse{}, fmt.Errorf("revelt pool: acquiring worker: %w", err)
	}

	return w.render(ctx, req)
}

// workerAt returns the worker at index idx, replacing it first if it has died.
func (p *pool) workerAt(idx int) (*worker, error) {
	// Fast path: worker is alive.
	p.mu.RLock()
	w := p.workers[idx]
	p.mu.RUnlock()

	if w.alive() {
		return w, nil
	}

	// Slow path: replace the dead worker.
	p.mu.Lock()
	defer p.mu.Unlock()

	// Re-check under the write lock — another goroutine may have already
	// performed the replacement while we were waiting.
	if p.workers[idx].alive() {
		return p.workers[idx], nil
	}

	newW, err := newWorker(p.ctx, p.script, p.cfg)
	if err != nil {
		return nil, fmt.Errorf("replacing dead worker at slot %d: %w", idx, err)
	}
	p.workers[idx] = newW
	return newW, nil
}

// close gracefully shuts down all workers.
func (p *pool) close() error {
	return p.closeRange(0, len(p.workers))
}

// closeRange shuts down workers[start:end]. Used both during partial
// initialisation failures and during full pool shutdown.
func (p *pool) closeRange(start, end int) error {
	var firstErr error
	for i := start; i < end; i++ {
		if err := p.workers[i].close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("revelt pool: closing worker %d: %w", i, err)
		}
	}
	return firstErr
}

// stats returns a snapshot of per-worker liveness. Useful for health endpoints
// and diagnostic logging.
func (p *pool) stats() []WorkerStat {
	p.mu.RLock()
	defer p.mu.RUnlock()

	out := make([]WorkerStat, len(p.workers))
	for i, w := range p.workers {
		out[i] = WorkerStat{
			Index:  i,
			Alive:  w.alive(),
			Stderr: w.stderr(),
		}
	}
	return out
}
