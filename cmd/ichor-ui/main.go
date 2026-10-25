/*
Copyright 2026 maeshinshin.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// ichor-ui is the Go backend that embeds the Preact SPA (docs/design/architecture.md,
// "CLI and UI"). Phase 5 only builds the skeleton: static asset serving with an
// SPA fallback. It holds no Talos or kube credentials yet and has no
// Connect API client wired in; that lands with the API server and its generated
// types, which this package intentionally does not depend on.
package main

import (
	"context"
	"errors"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// mustSub roots an embedded FS at dir so callers can address files the way the
// browser does ("index.html", "assets/app.js"), regardless of which build tag
// populated webFS (see webfs_dev.go and webfs_embed.go).
func mustSub(f fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

// spaHandler serves files as a single-page app: real files pass through
// unchanged, anything else (a client-side route) falls back to index.html so
// the SPA's own router can handle it.
func spaHandler(files fs.FS) http.Handler {
	fileServer := http.FileServerFS(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "."
		}
		if _, err := fs.Stat(files, name); errors.Is(err, fs.ErrNotExist) {
			fallback := r.Clone(r.Context())
			fallback.URL.Path = "/"
			fileServer.ServeHTTP(w, fallback)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func main() {
	addr := flag.String("addr", ":8080", "address ichor-ui listens on")
	flag.Parse()

	mux := http.NewServeMux()
	mux.Handle("/", spaHandler(webFS))

	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("ichor-ui: listening on %s", *addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("ichor-ui: %v", err)
		}
	}()

	<-ctx.Done()

	// shortcut: graceful shutdown here only stops accepting new connections
	// and waits out in-flight requests. Architecture's "asks clients to
	// reconnect" and "streams that resume from a token" apply once this
	// backend has streaming endpoints (logs, watches) to drain; nothing to
	// drain yet in the phase-5 skeleton.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("ichor-ui: graceful shutdown failed: %v", err)
	}
}
