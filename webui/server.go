package webui

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"sync"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"
)

//go:embed static/*
var staticFS embed.FS

// Tunables for the mock QR pairing flow.
const (
	mockScanDelay = 5 * time.Second  // How long "pending" waits before a simulated scan succeeds.
	mockQRExpiry  = 60 * time.Second // How long a QR stays valid if never scanned.
	maxWindow     = 600              // Samples retained for the live chart / rolling stats.
)

// Server is the research governance dashboard. It is safe for concurrent use.
//
// It intentionally holds no WhatsApp client and drives no real probing: all
// telemetry is synthesized locally (see mock.go). Wiring it to a live,
// authorized measurement backend is deliberately out of scope for this package.
type Server struct {
	log waLog.Logger
	now func() time.Time

	mu sync.Mutex

	// Session (QR link) state.
	sessState   ConnectionState
	sessSince   time.Time
	sessQR      [][]bool
	sessOutcome ConnectionState // Terminal state a pending session resolves to (connected/expired/error).
	sessAccount string

	// Participant allowlist.
	participants []*Participant

	// Experiment state.
	expStatus    ExperimentStatus
	expConfig    *ExperimentConfig
	expTarget    *Participant
	expStartedAt time.Time
	expEstimated int
	expProbes    int
	expMessage   string
	samples      []Sample
	gen          *sampleGen
	stopCh       chan struct{}
}

// New creates a Server seeded with the mock allowlist.
func New(log waLog.Logger) *Server {
	if log == nil {
		log = waLog.Noop
	}
	return &Server{
		log:          log,
		now:          time.Now,
		sessState:    ConnIdle,
		participants: mockParticipants(),
		expStatus:    ExpIdle,
	}
}

// Handler returns the HTTP handler for the dashboard and its JSON API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	sub, _ := fs.Sub(staticFS, "static")
	// http.FileServer serves index.html for "/" automatically.
	mux.Handle("/", http.FileServer(http.FS(sub)))

	mux.HandleFunc("/api/session", s.handleSession)
	mux.HandleFunc("/api/session/connect", s.handleConnect)
	mux.HandleFunc("/api/session/disconnect", s.handleDisconnect)
	mux.HandleFunc("/api/participants", s.handleParticipants)
	mux.HandleFunc("/api/test-states", s.handleTestStates)
	mux.HandleFunc("/api/experiment", s.handleExperiment)
	mux.HandleFunc("/api/experiment/validate", s.handleValidate)
	mux.HandleFunc("/api/experiment/start", s.handleStart)
	mux.HandleFunc("/api/experiment/stop", s.handleStop)
	mux.HandleFunc("/api/telemetry", s.handleTelemetry)

	return securityHeaders(mux)
}

// securityHeaders sets a conservative CSP. The dashboard ships its own CSS/JS as
// separate static files, so no inline scripts or external origins are needed.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// ---- Session (QR) endpoints ------------------------------------------------

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.advanceSessionLocked()
	writeJSON(w, http.StatusOK, s.sessionStatusLocked())
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// A "simulate" hint lets the UI and tests exercise the non-happy terminal
	// states. It never affects real credentials; there are none.
	outcome := ConnConnected
	switch r.URL.Query().Get("simulate") {
	case "expired":
		outcome = ConnExpired
	case "error":
		outcome = ConnError
	}

	s.sessState = ConnPending
	s.sessSince = s.now()
	s.sessQR = mockQRMatrix(s.sessSince.UnixNano())
	s.sessOutcome = outcome
	s.sessAccount = "research-lab-01 (mock)"
	// SECURITY: never log the QR matrix, account identifier, or any token.
	s.log.Infof("QR pairing started (mock); awaiting scan")
	writeJSON(w, http.StatusOK, s.sessionStatusLocked())
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopExperimentLocked(ExpStopped, "Research account disconnected.")
	s.sessState = ConnDisconnected
	s.sessQR = nil
	s.sessAccount = ""
	s.log.Infof("Research account disconnected (mock)")
	writeJSON(w, http.StatusOK, s.sessionStatusLocked())
}

// advanceSessionLocked drives the mock QR state machine based on elapsed time.
func (s *Server) advanceSessionLocked() {
	if s.sessState != ConnPending {
		return
	}
	elapsed := s.now().Sub(s.sessSince)
	switch s.sessOutcome {
	case ConnError:
		if elapsed >= mockScanDelay {
			s.sessState = ConnError
			s.sessQR = nil
		}
	case ConnExpired:
		if elapsed >= mockQRExpiry {
			s.sessState = ConnExpired
			s.sessQR = nil
		}
	default: // ConnConnected
		if elapsed >= mockScanDelay {
			s.sessState = ConnConnected
			s.sessQR = nil
		} else if elapsed >= mockQRExpiry {
			s.sessState = ConnExpired
			s.sessQR = nil
		}
	}
}

func (s *Server) sessionStatusLocked() SessionStatus {
	st := SessionStatus{State: s.sessState, Mock: true}
	switch s.sessState {
	case ConnPending:
		remain := int((mockQRExpiry - s.now().Sub(s.sessSince)).Seconds())
		if remain < 0 {
			remain = 0
		}
		st.ExpiresInSec = remain
		st.QRMatrix = s.sessQR
		st.Message = "Scan the placeholder code to link the research account."
	case ConnConnected:
		st.Account = s.sessAccount
		st.Message = "Research account linked (mock). Live probing is not wired in this build."
	case ConnExpired:
		st.Message = "The pairing code expired. Generate a new one to try again."
	case ConnDisconnected:
		st.Message = "Disconnected. Reconnect to run experiments."
	case ConnError:
		st.Message = "Pairing failed. Check the integration and try again."
	default:
		st.Message = "Not connected. Link an approved research account to begin."
	}
	return st
}

// ---- Participant / metadata endpoints -------------------------------------

func (s *Server) handleParticipants(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	participants := s.participants
	now := s.now()
	s.mu.Unlock()

	type row struct {
		*Participant
		Eligible bool `json:"eligible"`
	}
	out := make([]row, 0, len(participants))
	for _, p := range participants {
		out = append(out, row{Participant: p, Eligible: p.Eligible(now)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleTestStates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, ApprovedTestStates)
}

// ---- Experiment endpoints --------------------------------------------------

func (s *Server) handleExperiment(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, s.experimentStateLocked())
}

func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	cfg, err := decodeConfig(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	s.mu.Lock()
	p := s.findParticipantLocked(cfg.ParticipantID)
	now := s.now()
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, ValidateConfig(cfg, p, now))
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	cfg, err := decodeConfig(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sessState != ConnConnected {
		writeErr(w, http.StatusPreconditionFailed, "Link a research account before starting an experiment.")
		return
	}
	if s.expStatus == ExpRunning {
		writeErr(w, http.StatusConflict, "An experiment is already running. Stop it first.")
		return
	}

	p := s.findParticipantLocked(cfg.ParticipantID)
	res := ValidateConfig(cfg, p, s.now())
	if !res.Valid {
		writeJSON(w, http.StatusUnprocessableEntity, res)
		return
	}

	c := cfg
	s.expConfig = &c
	s.expTarget = p
	s.expStatus = ExpRunning
	s.expStartedAt = s.now()
	s.expEstimated = res.EstimatedProbes
	s.expProbes = 0
	s.expMessage = ""
	s.samples = nil
	s.gen = newSampleGen(cfg.TestState, s.expStartedAt.UnixNano())
	s.stopCh = make(chan struct{})
	go s.runLoop(s.stopCh, time.Duration(cfg.IntervalMs)*time.Millisecond)

	s.log.Infof("Experiment started (mock): participant=%s state=%s est_probes=%d", p.ID, cfg.TestState, res.EstimatedProbes)
	writeJSON(w, http.StatusOK, s.experimentStateLocked())
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopExperimentLocked(ExpStopped, "Experiment halted by emergency stop.")
	s.log.Infof("Experiment emergency-stopped (mock)")
	writeJSON(w, http.StatusOK, s.experimentStateLocked())
}

func (s *Server) handleTelemetry(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, s.buildTelemetryLocked())
}

// ---- Experiment run loop ---------------------------------------------------

func (s *Server) runLoop(stop chan struct{}, interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	// Emit one sample immediately so the chart is not empty on first poll.
	s.tick()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if done := s.tick(); done {
				return
			}
		}
	}
}

// tick appends one mock sample and enforces the duration and iteration limits.
// It returns true when the run has reached a terminal (completed) state.
func (s *Server) tick() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.expStatus != ExpRunning || s.gen == nil || s.expConfig == nil {
		return true
	}

	now := s.now()
	s.samples = append(s.samples, s.gen.next(now.UnixMilli()))
	if len(s.samples) > maxWindow {
		s.samples = s.samples[len(s.samples)-maxWindow:]
	}
	s.expProbes++

	// Iteration limit.
	if s.expConfig.MaxProbes > 0 && s.expProbes >= s.expConfig.MaxProbes {
		s.finishLocked("Reached iteration limit.")
		return true
	}
	// Duration limit.
	if s.expConfig.DurationSec > 0 {
		if now.Sub(s.expStartedAt) >= time.Duration(s.expConfig.DurationSec)*time.Second {
			s.finishLocked("Reached configured duration.")
			return true
		}
	}
	return false
}

func (s *Server) finishLocked(msg string) {
	s.expStatus = ExpCompleted
	s.expMessage = msg
	if s.stopCh != nil {
		select {
		case <-s.stopCh:
		default:
			close(s.stopCh)
		}
		s.stopCh = nil
	}
}

func (s *Server) stopExperimentLocked(status ExperimentStatus, msg string) {
	if s.expStatus != ExpRunning {
		return
	}
	s.expStatus = status
	s.expMessage = msg
	if s.stopCh != nil {
		select {
		case <-s.stopCh:
		default:
			close(s.stopCh)
		}
		s.stopCh = nil
	}
}

// ---- Snapshot builders (must be called with s.mu held) ---------------------

func (s *Server) findParticipantLocked(id string) *Participant {
	for _, p := range s.participants {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func (s *Server) experimentStateLocked() ExperimentState {
	st := ExperimentState{
		Status:          s.expStatus,
		Config:          s.expConfig,
		Participant:     s.expTarget,
		ProbesSent:      s.expProbes,
		EstimatedProbes: s.expEstimated,
		Message:         s.expMessage,
	}
	if s.expConfig != nil {
		st.TestState = s.expConfig.TestState
	}
	if !s.expStartedAt.IsZero() {
		st.StartedAt = s.expStartedAt.UTC().Format(time.RFC3339)
	}
	return st
}

func (s *Server) buildTelemetryLocked() Telemetry {
	t := Telemetry{
		Connection:   s.sessState,
		Running:      s.expStatus == ExpRunning,
		Samples:      append([]Sample(nil), s.samples...),
		Bands:        Bands,
		Confounders:  mockConfounders(),
		Distribution: map[string]int{},
	}

	for _, b := range Bands {
		t.Distribution[b.Key] = 0
	}
	var lastSuccess *float64
	for _, sm := range s.samples {
		if sm.Success {
			t.Distribution[sm.BandKey]++
			v := sm.RTTMs
			lastSuccess = &v
		}
	}
	t.CurrentRTTMs = lastSuccess

	median, p95, count := stats(s.samples)
	t.MedianRTTMs = median
	t.P95RTTMs = p95
	t.Count = count

	// Confidence grows with sample count but is capped below 1 to keep the
	// framing honest: this is never certainty.
	t.Confidence = float64(count) / 200.0
	if t.Confidence > 0.9 {
		t.Confidence = 0.9
	}

	// Overlap: share of successful samples NOT in the modal band. High overlap
	// means the bands are not cleanly separable in this run.
	if count > 0 {
		modal := 0
		for _, c := range t.Distribution {
			if c > modal {
				modal = c
			}
		}
		t.Overlap = 1 - float64(modal)/float64(count)
	}

	t.UncertaintyNote = "RTT is an indirect signal. Bands are overlapping hypotheses, not confirmed device states; network RTT, packet loss, server load, and connection reuse all shift the measured value."
	t.VerifiedGroundTruth = nil // No ground truth is verified in this build.
	return t
}

// ---- HTTP helpers ----------------------------------------------------------

func decodeConfig(r *http.Request) (ExperimentConfig, error) {
	var cfg ExperimentConfig
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<16))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
