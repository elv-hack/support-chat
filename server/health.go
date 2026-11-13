package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

var buildVersion = "dev"

func (a *App) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	if a.db.PingContext(ctx) != nil {
		problem(w, 503, "database unavailable")
		return
	}
	if _, err := os.Stat(filepath.Join(a.cfg.WebDir, "index.html")); err != nil {
		problem(w, 503, "web assets unavailable")
		return
	}
	if _, err := a.im.maxSeqs(ctx, a.cfg.OpenIMAdmin); err != nil {
		problem(w, 503, "message service unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"status": "ready", "version": buildVersion, "schemaVersion": schemaVersion})
}
func healthcheck() error {
	client := &http.Client{Timeout: 6 * time.Second}
	r, err := client.Get("http://127.0.0.1:8090/api/ready")
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return fmt.Errorf("not ready: %d", r.StatusCode)
	}
	return nil
}
