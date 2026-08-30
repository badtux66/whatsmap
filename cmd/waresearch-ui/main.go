// Command waresearch-ui serves the WhatsMap timing-research governance console.
//
// The console is a consent-gated research dashboard that runs on MOCK data only.
// It does not link a WhatsApp account and does not drive the covert probing
// engine in the mapper package. Its purpose is to demonstrate and enforce the
// governance controls (participant allowlist, consent verification, safe
// experiment limits, emergency stop) that authorized timing research requires.
package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"
	"go.mau.fi/whatsmeow/webui"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "Address to listen on")
	logLevel := flag.String("log", "INFO", "Log level (DEBUG, INFO, WARN, ERROR)")
	flag.Parse()

	logger := waLog.Stdout("ResearchUI", *logLevel, true)
	server := webui.New(logger.Sub("Server"))

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Infof("Serving research governance console on http://%s (mock data only)", *addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Errorf("server error: %v", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Infof("Shutting down…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Warnf("graceful shutdown failed: %v", err)
	}
}
