package revelt

import (
	"bufio"         // for bufio.NewReaderSize
	"bytes"         // for bytes.Buffer (stderr capture)
	"context"       // for context.Context
	"encoding/json" // for json.Marshal, json.Unmarshal
	"fmt"           // for fmt.Errorf
	"io"            // for io.WriteCloser
	"os/exec"       // for exec.Cmd, exec.CommandContext
	"sync"          // for sync.Mutex, sync.WaitGroup
	"sync/atomic"   // for atomic.Uint64
	"time"          // for time.Duration

	"github.com/abiiranathan/revelt/protocol"
)

// pendingCall holds the reply channel for one in-flight render request.
type pendingCall struct {
	// resp receives exactly one RenderResponse from the reader goroutine.
	resp chan protocol.RenderResponse
}

// worker owns a single Node.js subprocess and multiplexes concurrent render
// requests over the process's stdin/stdout. A dedicated goroutine reads
// responses and dispatches them back to waiting callers by request ID.
//
// The zero value is not usable; construct with newWorker.
type worker struct {
	// cmd is the supervised Node process.
	cmd *exec.Cmd
	// stdin is the write end of the pipe connected to node's stdin.
	stdin io.WriteCloser

	// stderrBuf captures node's stderr so it never corrupts the JSON stream.
	stderrBuf bytes.Buffer

	// writeMu serialises concurrent writes to stdin. Reads are serialised by
	// the single reader goroutine; no read lock is needed.
	writeMu sync.Mutex

	// pending maps in-flight RequestIDs to their waiting goroutines.
	pendingMu sync.Mutex
	pending   map[protocol.RequestID]*pendingCall

	// nextID generates monotonically increasing request IDs.
	nextID atomic.Uint64

	// done is closed when the reader goroutine exits (process died or was shut
	// down). Any goroutine waiting on a response selects on this.
	done chan struct{}

	// readErr holds the terminal error from the reader goroutine, set before
	// done is closed.
	readErr error
}

// newWorker starts a new Node subprocess executing script and begins the
// background reader goroutine. The supplied context controls the process
// lifetime: cancelling it sends SIGKILL to the Node process.
func newWorker(ctx context.Context, script string, cfg workerConfig) (*worker, error) {
	cmd := exec.CommandContext(ctx, cfg.NodeBin, script)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("revelt worker: stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("revelt worker: stdout pipe: %w", err)
	}

	w := &worker{
		cmd:     cmd,
		stdin:   stdin,
		pending: make(map[protocol.RequestID]*pendingCall),
		done:    make(chan struct{}),
	}
	cmd.Stderr = &w.stderrBuf

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("revelt worker: starting node: %w", err)
	}

	// The reader goroutine owns stdout for its entire lifetime.
	go w.readLoop(bufio.NewReaderSize(stdout, cfg.ReadBufSize))

	return w, nil
}

// render sends a render request to the Node process and waits for the matching
// response. It respects the caller's context for timeout/cancellation.
//
// render is safe for concurrent use by multiple goroutines.
func (w *worker) render(ctx context.Context, req protocol.RenderRequest) (protocol.RenderResponse, error) {
	// Assign a unique ID and register a reply channel before writing, so the
	// reader goroutine can never deliver the response before we are waiting.
	req.ID = protocol.RequestID(w.nextID.Add(1))

	call := &pendingCall{
		resp: make(chan protocol.RenderResponse, 1),
	}
	w.pendingMu.Lock()
	w.pending[req.ID] = call
	w.pendingMu.Unlock()

	// Deregister on all exit paths so we never leak map entries.
	defer func() {
		w.pendingMu.Lock()
		delete(w.pending, req.ID)
		w.pendingMu.Unlock()
	}()

	// Serialise the write so concurrent callers don't interleave bytes on stdin.
	if err := w.writeRequest(req); err != nil {
		return protocol.RenderResponse{}, err
	}

	select {
	case resp := <-call.resp:
		return resp, nil
	case <-w.done:
		return protocol.RenderResponse{}, fmt.Errorf(
			"revelt worker: process exited: %w", w.readErr,
		)
	case <-ctx.Done():
		return protocol.RenderResponse{}, fmt.Errorf(
			"revelt worker: request cancelled: %w", ctx.Err(),
		)
	}
}

// writeRequest marshals req as a single newline-terminated JSON line and writes
// it atomically to the worker's stdin.
func (w *worker) writeRequest(req protocol.RenderRequest) error {
	line, err := json.Marshal(req)
	if err != nil {
		// This should never happen for a well-typed struct; treat as fatal.
		return fmt.Errorf("revelt worker: marshalling request: %w", err)
	}
	line = append(line, '\n')

	w.writeMu.Lock()
	defer w.writeMu.Unlock()

	if _, err := w.stdin.Write(line); err != nil {
		return fmt.Errorf("revelt worker: writing to node stdin: %w", err)
	}
	return nil
}

// readLoop is the single goroutine that reads responses from Node's stdout and
// dispatches them to waiting callers. It exits when the pipe is closed or an
// unrecoverable read/parse error occurs.
func (w *worker) readLoop(r *bufio.Reader) {
	defer close(w.done)

	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			// io.EOF is expected on clean shutdown (stdin closed → node exits).
			if err != io.EOF {
				w.readErr = fmt.Errorf("reading node stdout: %w", err)
			}
			// Drain any remaining pending callers so they unblock via w.done.
			w.drainPending(w.readErr)
			return
		}

		var resp protocol.RenderResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			// A parse failure on a single line is not fatal; log and continue.
			// The mismatched request will eventually time out on the caller side.
			// Production deployments should surface this via a metrics hook.
			continue
		}

		w.dispatch(resp)
	}
}

// dispatch delivers resp to the caller registered for resp.ID, if any.
func (w *worker) dispatch(resp protocol.RenderResponse) {
	w.pendingMu.Lock()
	call, ok := w.pending[resp.ID]
	w.pendingMu.Unlock()

	if !ok {
		// Stale response (caller already timed out and deregistered). Discard.
		return
	}

	// Non-blocking send: the channel is buffered(1) so this never blocks even
	// if the caller already left via context cancellation.
	select {
	case call.resp <- resp:
	default:
	}
}

// drainPending unblocks all in-flight callers by closing their response
// channels after injecting a terminal error response. Called when the reader
// goroutine exits.
func (w *worker) drainPending(cause error) {
	w.pendingMu.Lock()
	defer w.pendingMu.Unlock()

	errMsg := "worker exited"
	if cause != nil {
		errMsg = cause.Error()
	}

	for id, call := range w.pending {
		select {
		case call.resp <- protocol.RenderResponse{Error: errMsg}:
		default:
		}
		delete(w.pending, id)
	}
}

// close shuts the worker down: closes stdin (which triggers EOF in the Node
// process and causes it to exit cleanly), then waits for it to terminate.
func (w *worker) close() error {
	if err := w.stdin.Close(); err != nil {
		return fmt.Errorf("revelt worker: closing stdin: %w", err)
	}
	// Wait with no timeout; the caller's context passed to exec.CommandContext
	// is the right lever for hard timeouts.
	if err := w.cmd.Wait(); err != nil {
		return fmt.Errorf("revelt worker: node exited: %w", err)
	}
	return nil
}

// stderr returns the accumulated stderr output of the worker process. Useful
// for surfacing Node errors in diagnostics.
func (w *worker) stderr() string {
	return w.stderrBuf.String()
}

// alive reports whether the worker's reader goroutine is still running.
func (w *worker) alive() bool {
	select {
	case <-w.done:
		return false
	default:
		return true
	}
}

// workerConfig holds tunable parameters for individual workers. It is
// populated from Config by the pool and not part of the public API surface.
type workerConfig struct {
	// NodeBin is the path or name of the Node.js binary. Defaults to "node".
	NodeBin string

	// ReadBufSize is the size of the bufio.Reader wrapping Node's stdout.
	// Defaults to 64 KiB.
	ReadBufSize int

	// WriteTimeout is an optional per-write deadline applied when writing to
	// stdin. Zero means no timeout (the caller's context is the deadline).
	WriteTimeout time.Duration
}
