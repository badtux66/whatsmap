// Command waresearch-ui serves the WhatsMap timing-research governance console.
//
// The console is a consent-gated research dashboard. Experiment telemetry is
// MOCK: it does not drive the covert probing engine in the mapper package. Its
// purpose is to demonstrate and enforce the governance controls (participant
// consent allowlist, safe experiment limits, emergency stop) that authorized
// timing research requires.
//
// By default the console links the researcher's OWN WhatsApp account via the
// official "Linked devices" QR flow; that linked account is the only account.
// Linking only establishes the connection and never measures anyone — who may
// be measured is still gated entirely by the consent allowlist. Pass -mock for
// an offline demo that links nothing (useful for UI development without a phone
// or network).
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
	mock := flag.Bool("mock", false, "Offline demo: use a placeholder QR that links nothing (for UI development)")
	sessionDB := flag.String("session-db", "waresearch.db", "Path to the linked-account session database")
	flag.Parse()

	logger := waLog.Stdout("ResearchUI", *logLevel, true)

	var opts []webui.Option
	mode := "live WhatsApp QR (own account)"
	if *mock {
		mode = "offline mock QR (links nothing)"
	} else {
		linker := live.New(*sessionDB, logger.Sub("Link"))
		defer linker.Close()
		opts = append(opts, webui.WithLinker(linker))
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
