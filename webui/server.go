package webui

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
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
// Experiment telemetry is synthesized locally (see mock.go); the covert probing
// engine in the mapper package is intentionally not wired in. The account link
// is handled by a SessionLinker: the default mock linker pairs nothing, while
// the webui/live linker performs a real WhatsApp linked-device pairing of the
// researcher's *own* account. Either way, who may be measured is gated only by
// the participant consent allowlist, never by the link.
type Server struct {
	log    waLog.Logger
	now    func() time.Time
	linker SessionLinker

	mu sync.Mutex

	// Participant allowlist. rawContacts holds the (unexposed) raw number for
	// enrolled participants, keyed by participant ID.
	participants []*Participant
	rawContacts  map[string]string
	enrollSeq    int

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

// Option configures a Server.
type Option func(*Server)

// WithLinker replaces the default mock account linker (e.g. with a real
// WhatsApp linked-device linker from the webui/live package).
func WithLinker(l SessionLinker) Option {
	return func(s *Server) {
		if s.linker != nil {
			_ = s.linker.Close()
		}
		s.linker = l
	}
}

// New creates a Server seeded with the mock allowlist and mock account linker.
func New(log waLog.Logger, opts ...Option) *Server {
	if log == nil {
		log = waLog.Noop
	}
	s := &Server{
		log:          log,
		now:          time.Now,
		participants: mockParticipants(),
		rawContacts:  map[string]string{},
		expStatus:    ExpIdle,
	}
	s.linker = newMockLinker(s.now)
	for _, o := range opts {
		o(s)
	}
	return s
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
	// SECURITY: the linker never logs the pairing code or any token.
	writeJSON(w, http.StatusOK, s.linker.Status())
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	st := s.linker.Connect(r.URL.Query().Get("simulate"))
	s.log.Infof("Account pairing started (mock=%t); awaiting scan", st.Mock)
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	s.mu.Lock()
	s.stopExperimentLocked(ExpStopped, "Research account disconnected.")
	s.mu.Unlock()
	st := s.linker.Disconnect()
	s.log.Infof("Research account disconnected")
	writeJSON(w, http.StatusOK, st)
}

// ---- Participant / metadata endpoints -------------------------------------

func (s *Server) handleParticipants(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listParticipants(w)
	case http.MethodPost:
		s.enrollParticipant(w, r)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "GET or POST required")
	}
}

type participantRow struct {
	*Participant
	Eligible bool `json:"eligible"`
}

func (s *Server) listParticipants(w http.ResponseWriter) {
	s.mu.Lock()
	participants := append([]*Participant(nil), s.participants...)
	now := s.now()
	s.mu.Unlock()

	out := make([]participantRow, 0, len(participants))
	for _, p := range participants {
		out = append(out, participantRow{Participant: p, Eligible: p.Eligible(now)})
	}
	writeJSON(w, http.StatusOK, out)
}

// enrollRequest is the body for enrolling any phone number. Enrollment is the
// consent gate: a number becomes targetable only once the researcher attests to
// ownership or documented consent and supplies a reference.
type enrollRequest struct {
	Label       string `json:"label"`
	Contact     string `json:"contact"`
	Basis       string `json:"basis"` // "ownership" | "consent"
	Reference   string `json:"reference"`
	Attestation bool   `json:"attestation"`
}

func (s *Server) enrollParticipant(w http.ResponseWriter, r *http.Request) {
	var req enrollRequest
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<16))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	var errs []string
	if !req.Attestation {
		errs = append(errs, "You must confirm this is a device you own or a participant who has given documented consent.")
	}
	digits := digitsOnly(req.Contact)
	if len(digits) < 6 {
		errs = append(errs, "Enter a valid phone number (with country code).")
	}
	switch req.Basis {
	case "ownership":
		// A reference is optional for a device you own.
	case "consent":
		if req.Reference == "" {
			errs = append(errs, "A consent/authorization reference is required for a consenting participant.")
		}
	default:
		errs = append(errs, "Select a basis: device ownership or documented consent.")
	}
	if len(errs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{"errors": errs})
		return
	}

	s.mu.Lock()
	s.enrollSeq++
	id := fmt.Sprintf("u-%03d", s.enrollSeq)
	ref := req.Reference
	if req.Basis == "ownership" && ref == "" {
		ref = "self-owned device"
	}
	label := req.Label
	if label == "" {
		label = "Participant " + maskContact(digits)
	}
	p := &Participant{
		ID:                id,
		Label:             label,
		MaskedContact:     maskContact(digits),
		ConsentStatus:     ConsentVerified,
		ConsentRef:        ref,
		OwnershipVerified: req.Basis == "ownership",
		ConsentExpiry:     "",
		Enrolled:          true,
	}
	s.participants = append(s.participants, p)
	s.rawContacts[id] = digits // Stored server-side, never returned to clients.
	now := s.now()
	s.mu.Unlock()

	// SECURITY: log the participant ID and basis only, never the raw number.
	s.log.Infof("Enrolled participant %s (basis=%s)", id, req.Basis)
	writeJSON(w, http.StatusOK, participantRow{Participant: p, Eligible: p.Eligible(now)})
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

	if s.linker.Status().State != ConnConnected {
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
		Connection:   s.linker.Status().State,
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

// digitsOnly strips everything but digits from a phone number input.
func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// maskContact returns a privacy-preserving rendering of a phone number that
// keeps only a short prefix and the last two digits, e.g. "+1 415•••••34".
func maskContact(digits string) string {
	if len(digits) <= 4 {
		return "•••" + digits
	}
	prefix := digits[:len(digits)-2]
	last := digits[len(digits)-2:]
	shown := prefix
	if len(prefix) > 4 {
		shown = prefix[:4]
	}
	return "+" + shown + strings.Repeat("•", len(digits)-len(shown)-2) + last
}
