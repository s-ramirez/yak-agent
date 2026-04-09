package webui

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
)

//go:embed static/*
var staticFS embed.FS

func (w *WebUI) startServer() {
	mux := http.NewServeMux()

	sub, _ := fs.Sub(staticFS, "static")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/events", w.handleSSE)
	mux.HandleFunc("/state", w.handleState)

	w.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", w.port),
		Handler: mux,
	}
	w.server.ListenAndServe()
}

func (w *WebUI) handleSSE(rw http.ResponseWriter, r *http.Request) {
	flusher, ok := rw.(http.Flusher)
	if !ok {
		http.Error(rw, "streaming not supported", http.StatusInternalServerError)
		return
	}

	rw.Header().Set("Content-Type", "text/event-stream")
	rw.Header().Set("Cache-Control", "no-cache")
	rw.Header().Set("Connection", "keep-alive")
	rw.Header().Set("Access-Control-Allow-Origin", "*")

	id, ch := w.bus.Subscribe()
	defer w.bus.Unsubscribe(id)

	// Send history for client catchup.
	for _, ev := range w.bus.History() {
		data, _ := json.Marshal(ev)
		fmt.Fprintf(rw, "data: %s\n\n", data)
	}
	flusher.Flush()

	for {
		select {
		case ev, open := <-ch:
			if !open {
				return
			}
			data, _ := json.Marshal(ev)
			fmt.Fprintf(rw, "data: %s\n\n", data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (w *WebUI) handleState(rw http.ResponseWriter, r *http.Request) {
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Access-Control-Allow-Origin", "*")

	history := w.bus.History()
	data, _ := json.Marshal(map[string]any{
		"events": history,
	})
	rw.Write(data)
}
