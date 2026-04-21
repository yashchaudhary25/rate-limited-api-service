package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"rate-limited-api/handler"
)

func main() {
	h := handler.New()
	defer h.Stop()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /request", h.HandleRequest)
	mux.HandleFunc("GET /stats", h.HandleStats)
	mux.HandleFunc("GET /health", handleHealth)

	addr := ":8080"
	// allow port override through env, otherwise use default
	if port := os.Getenv("PORT"); port != "" {
		addr = ":" + port
	}

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// listen for shutdown signals so in-flight requests get time to finish
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("server listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-quit
	log.Println("shutting down…")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("forced shutdown: %v", err)
	}
	log.Println("done")
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
