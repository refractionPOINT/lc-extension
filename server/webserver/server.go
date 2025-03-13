package webserver

import (
	"context"
	"fmt"
	"log"
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
		Addr:    fmt.Sprintf(":%d", port),
		Handler: extension,
	}

	wgServerClosed := sync.WaitGroup{}

	osSignals := make(chan os.Signal, 1)
	signal.Notify(osSignals, os.Interrupt, syscall.SIGTERM)

	wgServerClosed.Add(1)
	go func() {
		defer wgServerClosed.Done()
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			os.Stderr.Write([]byte(fmt.Sprintf("http.ListenAndServe(): %v\n", err)))

			// Try to terminat the process cleanly.
			p, err := os.FindProcess(os.Getpid())
			if err != nil {
				panic(err)
			}
			p.Signal(syscall.SIGTERM)
		}
	}()

	log.Println("server is listening on port", port)

	sig := <-osSignals
	log.Printf("Received signal %v, shutting down server...\n", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("server.Shutdown(): %v\n", err)
	}

	log.Println("server gracefully shut down")
	wgServerClosed.Wait()
}
