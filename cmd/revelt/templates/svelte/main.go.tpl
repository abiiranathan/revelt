package main

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/abiiranathan/revelt/health"
	"github.com/abiiranathan/revelt/revelt"
)

const (
	SSReveltConfig = "revelt.json"
	SideCarScript  = "render-server.cjs"
	ClientDir      = "client"
)

func main() {
	cfg, err := revelt.LoadConfig(SSReveltConfig)
	if err != nil {
		log.Fatalf("failed to load configuration: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sidecarPath := filepath.Join(cfg.OutDir, SideCarScript)

	renderer, err := revelt.New(ctx, sidecarPath,
		revelt.WithWorkers(cfg.Workers),
		revelt.WithRenderTimeout(time.Duration(cfg.TimeoutMS)*time.Millisecond),
		revelt.WithProjectConfig(cfg),
	)
	if err != nil {
		log.Fatalf("failed to start revelt: %v", err)
	}
	defer renderer.Close()

	mux := http.NewServeMux()

	mux.Handle("/healthz", health.Liveness(renderer))
	mux.Handle("/readyz", health.Readiness(renderer))

	clientAssetsDir := filepath.Join(cfg.OutDir, ClientDir)
	fs := http.FileServer(http.Dir(clientAssetsDir))
	mux.Handle(cfg.StaticPrefix, http.StripPrefix(cfg.StaticPrefix, fs))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}

		indexHTML, err := os.ReadFile(indexPagePath(cfg))
		if err != nil {
			http.Error(w, "index.html not found: "+err.Error(), http.StatusInternalServerError)
			return
		}

		header, err := renderer.Render(r.Context(), revelt.RenderInput{
			Component: "Header",
			Props: map[string]any{
				"title": "Configurable revelt Application",
			},
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		counter, err := renderer.Render(r.Context(), revelt.RenderInput{
			Component: "Counter",
			Props: map[string]any{
				"title":   "Hydrated Component",
				"initial": 10,
			},
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		chart, err := renderer.Render(r.Context(), revelt.RenderInput{
			Component: "ClientChart",
			Props: map[string]any{
				"label": "Client-Only Virtual Chart Component",
			},
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		t, err := template.New("index").Parse(string(indexHTML))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		buf := new(bytes.Buffer)
		err = t.Execute(buf, map[string]any{
			"Header":       template.HTML(header.HTML),
			"Counter":      template.HTML(counter.HTML),
			"ClientChart":  template.HTML(chart.HTML),
			"StaticPrefix": cfg.StaticPrefix,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write(buf.Bytes())
	})

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	log.Printf("Application running on http://localhost:%d", cfg.Port)

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server failure: %v", err)
		}
	}()

	// Block until the context is cancelled by SIGINT or SIGTERM.
	<-ctx.Done()
	stop() // Release signal resources promptly.

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}

// indexPagePath returns the absolute path to the HTML template.
func indexPagePath(cfg *revelt.ProjectConfig) string {
    return filepath.Join(cfg.OutDir, ClientDir, "index.html")
}
