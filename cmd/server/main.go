package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/2CHILON/jobqueue/internal/api"
	"github.com/2CHILON/jobqueue/internal/store"
)
// version is set at build time: -ldflags "-X main.version=v1.2.3"
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	addr := ":" + getEnv("PORT", "8080")

	st, err := store.OpenSQLite(getEnv("DB_PATH", "jobqueue.db"))
	if err != nil {
		log.Fatalf("cannot open database: %v", err)
	}
	defer st.Close()
	srv := api.NewServer(st)

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Run the server in a goroutine so main can wait for a shutdown signal.
	go func() {
		log.Printf("jobqueue %s listening on %s", version, addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Block until Ctrl+C (or a termination signal from Docker/Kubernetes).
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Fatalf("forced shutdown: %v", err)
	}
	log.Println("stopped cleanly")
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
