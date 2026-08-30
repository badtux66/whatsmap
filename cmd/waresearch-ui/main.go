// Command waresearch-ui serves the WhatsMap timing-research governance console.
//
// The console is a consent-gated research dashboard. Experiment telemetry is
// MOCK: it does not drive the covert probing engine in the mapper package. Its
// purpose is to demonstrate and enforce the governance controls (participant
// consent allowlist, safe experiment limits, emergency stop) that authorized
// timing research requires.
//
// By default the QR login is also a mock. Pass -live to link the researcher's
// OWN WhatsApp account via the official "Linked devices" QR flow; this only
// establishes the account link and never measures anyone — who may be measured
// is still gated entirely by the consent allowlist.
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

	_ "github.com/mattn/go-sqlite3"

	waLog "go.mau.fi/whatsmeow/util/log"
	"go.mau.fi/whatsmeow/webui"
	"go.mau.fi/whatsmeow/webui/live"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "Address to listen on")
	logLevel := flag.String("log", "INFO", "Log level (DEBUG, INFO, WARN, ERROR)")
	liveLink := flag.Bool("live", false, "Link a real WhatsApp account (your own) via QR instead of the mock flow")
	sessionDB := flag.String("session-db", "waresearch.db", "Path to the linked-account session database (with -live)")
	flag.Parse()

	logger := waLog.Stdout("ResearchUI", *logLevel, true)

	var opts []webui.Option
	mode := "mock QR"
	if *liveLink {
		linker := live.New(*sessionDB, logger.Sub("Link"))
		defer linker.Close()
		opts = append(opts, webui.WithLinker(linker))
		mode = "live WhatsApp QR (own account)"
	}
	server := webui.New(logger.Sub("Server"), opts...)

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Infof("Serving research governance console on http://%s (%s; experiment telemetry is mock)", *addr, mode)
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
