package webserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
)

func RunExtension(extension http.Handler) {
	port := 80
	if p := os.Getenv("PORT"); p != "" {
		p, err := strconv.ParseInt(p, 10, 16)
		if err != nil {
			panic(err)
		}
		port = int(p)
	}
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           extension,
		ReadHeaderTimeout: 5 * time.Second,
	}

	wgServerClosed := sync.WaitGroup{}

	osSignals := make(chan os.Signal, 1)
	signal.Notify(osSignals, os.Interrupt, syscall.SIGTERM)

	wgServerClosed.Add(1)
	go func() {
		defer wgServerClosed.Done()
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error(fmt.Sprintf("http.ListenAndServe(): %v\n", err))

			// Try to terminat the process cleanly.
			p, err := os.FindProcess(os.Getpid())
			if err != nil {
				panic(err)
			}
			if err := p.Signal(syscall.SIGTERM); err != nil {
				slog.Error(fmt.Sprintf("failed to send SIGTERM: %v", err))
				return
			}
		}
	}()

	slog.Info(fmt.Sprintf("server is listening on port %d", port))

	sig := <-osSignals
	slog.Info(fmt.Sprintf("Received signal %v, shutting down server...\n", sig))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Info(fmt.Sprintf("server.Shutdown(): %v\n", err))
	}

	slog.Info("server gracefully shut down")

	wgServerClosed.Wait()
}
