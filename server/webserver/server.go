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

// Timeouts of the HTTP server. They bound how long a connection can be held
// open by a peer that is slow, or that stops, sending or reading, which on a
// service reachable by anyone is otherwise unbounded.
//
// They are sized from the longest legitimate exchange rather than from typical
// ones: the extension manager gives up on a request after 2 minutes, so a
// request is fully read well within ReadTimeout; WriteTimeout covers the
// handler's run plus the response and is above the longest request timeout
// extensions are deployed with (15 minutes), so it never cuts a handler the
// platform is still waiting on.
const (
	readTimeout       = 2 * time.Minute
	writeTimeout      = 15 * time.Minute
	idleTimeout       = 620 * time.Second // above the 10 minutes a Google load balancer keeps an upstream connection
	readHeaderTimeout = 5 * time.Second
)

func newServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
}

func RunExtension(extension http.Handler) {
	port := 80
	if p := os.Getenv("PORT"); p != "" {
		p, err := strconv.ParseInt(p, 10, 16)
		if err != nil {
			panic(err)
		}
		port = int(p)
	}
	srv := newServer(fmt.Sprintf(":%d", port), extension)

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
