// Package health provides HTTP handler functions that expose the liveness and
// readiness of a [revelt.Renderer]'s worker pool.
//
// Mount them in any [net/http] mux:
//
//	mux.Handle("/healthz", health.Liveness(renderer))
//	mux.Handle("/readyz",  health.Readiness(renderer))
package health

import (
	"encoding/json" // for json.NewEncoder
	"net/http"      // for http.Handler, http.ResponseWriter, http.Request

	"github.com/abiiranathan/revelt/revelt"
)

// poolStatter is satisfied by *revelt.Renderer. Defined here as a narrow
// interface so that tests can provide a stub without importing the full package.
type poolStatter interface {
	Stats() []revelt.WorkerStat
}

// livenessResponse is the JSON body written by the liveness handler.
type livenessResponse struct {
	// Status is always "ok" for the liveness handler; a failing process would
	// not be able to respond at all.
	Status string `json:"status"`
	// Workers is a per-worker detail slice.
	Workers []revelt.WorkerStat `json:"workers"`
}

// readinessResponse is the JSON body written by the readiness handler.
type readinessResponse struct {
	// Status is "ok" when at least one worker is alive, "degraded" otherwise.
	Status string `json:"status"`
	// AliveWorkers is the count of workers whose Node process is running.
	AliveWorkers int `json:"alive_workers"`
	// TotalWorkers is the total pool size.
	TotalWorkers int `json:"total_workers"`
	// Workers is a per-worker detail slice.
	Workers []revelt.WorkerStat `json:"workers"`
}

// Liveness returns an HTTP handler that always responds 200 OK as long as the
// Go process is alive, with a JSON body listing per-worker state. It is
// intended for Kubernetes liveness probes; the probe should restart the pod
// only when this endpoint is unreachable, not when workers are degraded.
func Liveness(r poolStatter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body := livenessResponse{
			Status:  "ok",
			Workers: r.Stats(),
		}
		writeJSON(w, http.StatusOK, body)
	})
}

// Readiness returns an HTTP handler that responds 200 OK when at least one
// worker is alive, and 503 Service Unavailable when all workers are dead. It
// is intended for Kubernetes readiness probes; a 503 removes the pod from the
// load-balancer rotation until workers recover.
func Readiness(r poolStatter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		stats := r.Stats()

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

		body := readinessResponse{
			Status:       status,
			AliveWorkers: alive,
			TotalWorkers: len(stats),
			Workers:      stats,
		}
		writeJSON(w, code, body)
	})
}

// writeJSON serialises v as JSON and writes it to w with the supplied status
// code. Errors are silently ignored because the HTTP headers are already sent.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
