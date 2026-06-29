package main

import (
	"context"
	"log"
	"net/http"

	"github.com/abiiranathan/revelt/revelt"
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
				"title": "revelt Application",
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
