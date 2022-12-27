package webserver

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
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

	<-osSignals
	srv.Shutdown(context.Background())

	wgServerClosed.Wait()
}
