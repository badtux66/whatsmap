package webui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"
)

var refNow = time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

func validConfig() ExperimentConfig {
	return ExperimentConfig{
		ParticipantID: "p-001",
		DurationSec:   120,
		IntervalMs:    2000,
		MaxProbes:     30,
		TimeoutMs:     5000,
		TestState:     "foreground_baseline",
	}
}

func eligibleParticipant() *Participant {
	return &Participant{
		ID: "p-001", Label: "Lab device A", ConsentStatus: ConsentVerified,
		ConsentRef: "IRB-2026-014", OwnershipVerified: true,
		ConsentExpiry: "2027-01-01", Enrolled: true,
	}
}

// ---- Validation ------------------------------------------------------------

func TestValidateConfig_Valid(t *testing.T) {
	res := ValidateConfig(validConfig(), eligibleParticipant(), refNow)
	if !res.Valid {
		t.Fatalf("expected valid, got errors: %v", res.Errors)
	}
	if res.EstimatedProbes != 30 {
		t.Errorf("estimated probes = %d, want 30 (capped by MaxProbes)", res.EstimatedProbes)
	}
	if res.EffectiveRate != 0.5 {
		t.Errorf("effective rate = %v, want 0.5", res.EffectiveRate)
	}
}

func TestValidateConfig_SafetyLimits(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*ExperimentConfig)
		wantSub string
	}{
		{"interval too fast", func(c *ExperimentConfig) { c.IntervalMs = 250 }, "interval must be at least"},
		{"interval too slow", func(c *ExperimentConfig) { c.IntervalMs = MaxIntervalMs + 1 }, "interval must be at most"},
		{"duration too short", func(c *ExperimentConfig) { c.DurationSec = 3 }, "duration must be at least"},
		{"duration too long", func(c *ExperimentConfig) { c.DurationSec = MaxDurationSec + 1 }, "duration must be at most"},
		{"timeout too short", func(c *ExperimentConfig) { c.TimeoutMs = 100 }, "timeout must be at least"},
		{"timeout too long", func(c *ExperimentConfig) { c.TimeoutMs = MaxTimeoutMs + 1 }, "timeout must be at most"},
		{"negative iterations", func(c *ExperimentConfig) { c.MaxProbes = -1 }, "cannot be negative"},
		{"iterations over cap", func(c *ExperimentConfig) { c.MaxProbes = MaxProbesCap + 1 }, "iteration limit must be at most"},
		{"unapproved test state", func(c *ExperimentConfig) { c.TestState = "covert" }, "not an approved test state"},
		{"missing test state", func(c *ExperimentConfig) { c.TestState = "" }, "select an approved test state"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig()
			tc.mutate(&c)
			res := ValidateConfig(c, eligibleParticipant(), refNow)
			if res.Valid {
				t.Fatalf("expected invalid config")
			}
			if !containsSub(res.Errors, tc.wantSub) {
				t.Errorf("errors %v missing substring %q", res.Errors, tc.wantSub)
			}
		})
	}
}

func TestValidateConfig_ProbeCapFromDuration(t *testing.T) {
	c := validConfig()
	c.MaxProbes = 0 // Bounded only by duration.
	c.DurationSec = MaxDurationSec
	c.IntervalMs = MinIntervalMs
	res := ValidateConfig(c, eligibleParticipant(), refNow)
	// 6h at 1 probe/s = 21600 probes > cap.
	if res.Valid {
		t.Fatalf("expected invalid: duration-derived probes exceed cap, got est=%d", res.EstimatedProbes)
	}
	if !containsSub(res.Errors, "above the") {
		t.Errorf("expected cap error, got %v", res.Errors)
	}
}

// ---- Allowlist / consent enforcement --------------------------------------

func TestValidateConfig_RejectsUnknownAndNonConsenting(t *testing.T) {
	cases := []struct {
		name    string
		p       *Participant
		wantSub string
	}{
		{"nil/unknown", nil, "unknown participant"},
		{"pending", &Participant{ID: "p", ConsentStatus: ConsentPending, Enrolled: true}, "still pending"},
		{"expired", &Participant{ID: "p", ConsentStatus: ConsentExpired, ConsentRef: "x", ConsentExpiry: "2020-01-01", Enrolled: true}, "has expired"},
		{"revoked", &Participant{ID: "p", ConsentStatus: ConsentRevoked, Enrolled: true}, "has been revoked"},
		{"verified but no consent doc or ownership", &Participant{ID: "p", ConsentStatus: ConsentVerified, Enrolled: true}, "not eligible"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig()
			res := ValidateConfig(c, tc.p, refNow)
			if res.Valid {
				t.Fatalf("expected rejection for %s", tc.name)
			}
			if !containsSub(res.Errors, tc.wantSub) {
				t.Errorf("errors %v missing %q", res.Errors, tc.wantSub)
			}
		})
	}
}

func TestValidateConfig_MissingParticipantID(t *testing.T) {
	c := validConfig()
	c.ParticipantID = ""
	res := ValidateConfig(c, nil, refNow)
	if res.Valid || !containsSub(res.Errors, "select a research participant") {
		t.Errorf("expected participant-required error, got %v", res.Errors)
	}
}

func TestParticipantEligible(t *testing.T) {
	p := eligibleParticipant()
	if !p.Eligible(refNow) {
		t.Error("expected eligible")
	}
	expired := *p
	expired.ConsentExpiry = "2026-01-01"
	if expired.Eligible(refNow) {
		t.Error("expected ineligible after expiry")
	}
	consentByOwnership := &Participant{ID: "x", ConsentStatus: ConsentVerified, OwnershipVerified: true, Enrolled: true}
	if !consentByOwnership.Eligible(refNow) {
		t.Error("ownership-verified with no expiry should be eligible")
	}
	noProof := &Participant{ID: "y", ConsentStatus: ConsentVerified, Enrolled: true}
	if noProof.Eligible(refNow) {
		t.Error("verified but no consent ref and no ownership should be ineligible")
	}
}

// ---- Probe estimation ------------------------------------------------------

func TestEstimateProbes(t *testing.T) {
	tests := []struct {
		c    ExperimentConfig
		want int
	}{
		{ExperimentConfig{DurationSec: 120, IntervalMs: 2000, MaxProbes: 30}, 30}, // capped by max
		{ExperimentConfig{DurationSec: 120, IntervalMs: 2000, MaxProbes: 0}, 60},  // by duration
		{ExperimentConfig{DurationSec: 10, IntervalMs: 1000, MaxProbes: 100}, 10}, // duration smaller
		{ExperimentConfig{DurationSec: 100, IntervalMs: 0, MaxProbes: 5}, 0},      // no interval
	}
	for _, tc := range tests {
		if got := tc.c.EstimateProbes(); got != tc.want {
			t.Errorf("EstimateProbes(%+v) = %d, want %d", tc.c, got, tc.want)
		}
	}
}

func TestBandFor(t *testing.T) {
	cases := map[float64]string{
		100: "foreground", 299: "foreground", 300: "screen_on", 999: "screen_on",
		1000: "screen_off", 2999: "screen_off", 3000: "doze", 9000: "doze",
	}
	for rtt, want := range cases {
		if got := BandFor(rtt); got != want {
			t.Errorf("BandFor(%v) = %s, want %s", rtt, got, want)
		}
	}
}

// ---- HTTP: session state machine ------------------------------------------

func newTestServer() (*Server, func() time.Time, *time.Time) {
	s := New(nil)
	cur := refNow
	s.now = func() time.Time { return cur }
	s.linker = newMockLinker(s.now) // Use the test clock for the QR flow too.
	return s, s.now, &cur
}

func TestSessionStateMachine(t *testing.T) {
	s, _, cur := newTestServer()
	h := s.Handler()

	// Connect → pending with QR, no secrets logged.
	got := doJSON[SessionStatus](t, h, http.MethodPost, "/api/session/connect", nil)
	if got.State != ConnPending {
		t.Fatalf("state = %s, want pending", got.State)
	}
	if len(got.QRMatrix) == 0 {
		t.Error("expected placeholder QR matrix")
	}
	if got.ExpiresInSec <= 0 {
		t.Error("expected positive expiry countdown")
	}

	// Advance past scan delay → connected, QR cleared.
	*cur = cur.Add(mockScanDelay + time.Second)
	got = doJSON[SessionStatus](t, h, http.MethodGet, "/api/session", nil)
	if got.State != ConnConnected {
		t.Fatalf("state = %s, want connected", got.State)
	}
	if len(got.QRMatrix) != 0 {
		t.Error("QR should be cleared once connected")
	}

	// Disconnect.
	got = doJSON[SessionStatus](t, h, http.MethodPost, "/api/session/disconnect", nil)
	if got.State != ConnDisconnected {
		t.Fatalf("state = %s, want disconnected", got.State)
	}
}

func TestSessionSimulateExpiryAndError(t *testing.T) {
	s, _, cur := newTestServer()
	h := s.Handler()

	doJSON[SessionStatus](t, h, http.MethodPost, "/api/session/connect?simulate=expired", nil)
	*cur = cur.Add(mockQRExpiry + time.Second)
	got := doJSON[SessionStatus](t, h, http.MethodGet, "/api/session", nil)
	if got.State != ConnExpired {
		t.Errorf("state = %s, want expired", got.State)
	}

	*cur = refNow
	doJSON[SessionStatus](t, h, http.MethodPost, "/api/session/connect?simulate=error", nil)
	*cur = cur.Add(mockScanDelay + time.Second)
	got = doJSON[SessionStatus](t, h, http.MethodGet, "/api/session", nil)
	if got.State != ConnError {
		t.Errorf("state = %s, want error", got.State)
	}
}

// ---- HTTP: experiment start gating ----------------------------------------

func TestStart_RequiresConnection(t *testing.T) {
	s, _, _ := newTestServer()
	h := s.Handler()
	rec := doRaw(t, h, http.MethodPost, "/api/experiment/start", validConfig())
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412 (not connected)", rec.Code)
	}
}

func TestStart_RejectsInvalidConfigWhenConnected(t *testing.T) {
	s := connectedServer(t)
	h := s.Handler()

	bad := validConfig()
	bad.IntervalMs = 100 // Below safe minimum.
	rec := doRaw(t, h, http.MethodPost, "/api/experiment/start", bad)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}

	badTarget := validConfig()
	badTarget.ParticipantID = "p-004" // Consent pending in mock allowlist.
	rec = doRaw(t, h, http.MethodPost, "/api/experiment/start", badTarget)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 for non-consenting target", rec.Code)
	}
}

func TestStart_StopEmergency(t *testing.T) {
	s := connectedServer(t)
	h := s.Handler()

	st := doJSON[ExperimentState](t, h, http.MethodPost, "/api/experiment/start", validConfig())
	if st.Status != ExpRunning {
		t.Fatalf("status = %s, want running", st.Status)
	}
	if st.EstimatedProbes != 30 {
		t.Errorf("estimated = %d, want 30", st.EstimatedProbes)
	}

	// Second start should conflict.
	rec := doRaw(t, h, http.MethodPost, "/api/experiment/start", validConfig())
	if rec.Code != http.StatusConflict {
		t.Errorf("second start status = %d, want 409", rec.Code)
	}

	st = doJSON[ExperimentState](t, h, http.MethodPost, "/api/experiment/stop", nil)
	if st.Status != ExpStopped {
		t.Fatalf("status = %s, want stopped", st.Status)
	}
}

// ---- Experiment tick loop & telemetry --------------------------------------

func TestTickAppendsSamplesAndCompletesOnMaxProbes(t *testing.T) {
	s := connectedServer(t)
	cfg := validConfig()
	cfg.MaxProbes = 5
	if _, err := s.startForTest(cfg); err != nil {
		t.Fatal(err)
	}
	var done bool
	for i := 0; i < 10 && !done; i++ {
		done = s.tick()
	}
	if !done {
		t.Fatal("expected completion after reaching max probes")
	}
	s.mu.Lock()
	status, probes := s.expStatus, s.expProbes
	s.mu.Unlock()
	if status != ExpCompleted {
		t.Errorf("status = %s, want completed", status)
	}
	if probes != 5 {
		t.Errorf("probes = %d, want 5", probes)
	}
}

func TestTelemetryFraming(t *testing.T) {
	s := connectedServer(t)
	cfg := validConfig()
	cfg.MaxProbes = 40
	cfg.TestState = "foreground_baseline"
	if _, err := s.startForTest(cfg); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		s.tick()
	}
	tel := doJSON[Telemetry](t, s.Handler(), http.MethodGet, "/api/telemetry", nil)

	if tel.VerifiedGroundTruth != nil {
		t.Error("verified ground truth must be nil (RTT is indirect)")
	}
	if tel.Confidence > 0.9 {
		t.Errorf("confidence %v must be capped at 0.9", tel.Confidence)
	}
	if tel.Count == 0 || tel.MedianRTTMs == nil || tel.P95RTTMs == nil {
		t.Error("expected populated stats after ticks")
	}
	if len(tel.Bands) != 4 {
		t.Errorf("expected 4 hypothesis bands, got %d", len(tel.Bands))
	}
	for _, b := range tel.Bands {
		if !strings.HasPrefix(b.Hypothesis, "possible") {
			t.Errorf("band %q hypothesis %q must be hedged with 'possible'", b.Key, b.Hypothesis)
		}
	}
	if len(tel.Confounders) == 0 {
		t.Error("expected confounders to be reported")
	}
	if tel.UncertaintyNote == "" {
		t.Error("expected an uncertainty note")
	}
}

// ---- Enrollment (any number + consent gate) --------------------------------

func TestEnroll_RequiresAttestationAndReference(t *testing.T) {
	cases := []struct {
		name string
		body enrollRequest
		sub  string
	}{
		{"no attestation", enrollRequest{Contact: "14155551234", Basis: "ownership", Attestation: false}, "confirm"},
		{"short number", enrollRequest{Contact: "123", Basis: "ownership", Attestation: true}, "valid phone number"},
		{"consent without reference", enrollRequest{Contact: "14155551234", Basis: "consent", Attestation: true}, "reference is required"},
		{"missing basis", enrollRequest{Contact: "14155551234", Basis: "", Attestation: true}, "select a basis"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(nil)
			rec := doRaw(t, s.Handler(), http.MethodPost, "/api/participants", tc.body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422", rec.Code)
			}
			var resp struct {
				Errors []string `json:"errors"`
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &resp)
			if !containsSub(resp.Errors, tc.sub) {
				t.Errorf("errors %v missing %q", resp.Errors, tc.sub)
			}
		})
	}
}

func TestEnroll_OwnershipBecomesTargetable(t *testing.T) {
	s := connectedServer(t)
	h := s.Handler()

	row := doJSON[participantRow](t, h, http.MethodPost, "/api/participants",
		enrollRequest{Contact: "1 (415) 555-1234", Basis: "ownership", Attestation: true})
	if !row.Eligible {
		t.Fatalf("newly enrolled owned device should be eligible: %+v", row)
	}
	if row.ID == "" || row.ConsentStatus != ConsentVerified || !row.OwnershipVerified {
		t.Errorf("unexpected enrolled participant: %+v", row.Participant)
	}
	// Raw number must not be exposed; contact is masked.
	if strings.Contains(row.MaskedContact, "5551234") || !strings.Contains(row.MaskedContact, "•") {
		t.Errorf("contact not masked: %q", row.MaskedContact)
	}

	// It now appears in the list and an experiment can target it.
	list := doJSON[[]participantRow](t, h, http.MethodGet, "/api/participants", nil)
	found := false
	for _, p := range list {
		if p.ID == row.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("enrolled participant not in list")
	}

	cfg := validConfig()
	cfg.ParticipantID = row.ID
	st := doJSON[ExperimentState](t, h, http.MethodPost, "/api/experiment/start", cfg)
	if st.Status != ExpRunning {
		t.Fatalf("experiment on enrolled participant should start, got %s", st.Status)
	}
}

func TestEnroll_RawNumberNotLogged(t *testing.T) {
	var buf bytes.Buffer
	s := New(&captureLogger{buf: &buf})
	doRaw(t, s.Handler(), http.MethodPost, "/api/participants",
		enrollRequest{Contact: "14155559999", Basis: "ownership", Attestation: true})
	if strings.Contains(buf.String(), "14155559999") {
		t.Errorf("raw number leaked into logs: %q", buf.String())
	}
}

func TestMaskContact(t *testing.T) {
	cases := map[string]string{
		"14155551234": "+1415•••••34",
		"1234":        "•••1234",
	}
	for in, want := range cases {
		if got := maskContact(in); got != want {
			t.Errorf("maskContact(%q) = %q, want %q", in, got, want)
		}
	}
}

// ---- No-secret-logging -----------------------------------------------------

func TestConnectDoesNotLogQROrToken(t *testing.T) {
	var buf bytes.Buffer
	s := New(&captureLogger{buf: &buf})
	s.now = func() time.Time { return refNow }
	s.linker = newMockLinker(s.now)
	h := s.Handler()
	got := doJSON[SessionStatus](t, h, http.MethodPost, "/api/session/connect", nil)

	// The QR matrix returned for on-screen rendering must never appear in logs.
	logged := buf.String()
	if strings.Contains(logged, "true") && strings.Contains(logged, "false") {
		// Heuristic: a serialized matrix would contain many booleans.
		t.Errorf("log output looks like it contains QR data: %q", logged)
	}
	if strings.Contains(logged, "research-lab-01") {
		t.Errorf("account identifier leaked into logs: %q", logged)
	}
	if len(got.QRMatrix) == 0 {
		t.Error("expected QR matrix in API response (not logs)")
	}
}

// ---- helpers ---------------------------------------------------------------

// startForTest validates + starts an experiment without launching the ticker
// goroutine, so tests can drive tick() deterministically.
func (s *Server) startForTest(cfg ExperimentConfig) (ExperimentState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.findParticipantLocked(cfg.ParticipantID)
	res := ValidateConfig(cfg, p, s.now())
	if !res.Valid {
		return ExperimentState{}, &validationError{res.Errors}
	}
	c := cfg
	s.expConfig = &c
	s.expTarget = p
	s.expStatus = ExpRunning
	s.expStartedAt = s.now()
	s.expEstimated = res.EstimatedProbes
	s.expProbes = 0
	s.samples = nil
	s.gen = newSampleGen(cfg.TestState, 1)
	s.stopCh = make(chan struct{})
	return s.experimentStateLocked(), nil
}

type validationError struct{ errs []string }

func (e *validationError) Error() string { return strings.Join(e.errs, "; ") }

func connectedServer(t *testing.T) *Server {
	t.Helper()
	s := New(nil)
	cur := refNow
	s.now = func() time.Time { return cur }
	ml := newMockLinker(s.now)
	ml.forceConnected("research-lab-01 (mock)")
	s.linker = ml
	return s
}

type captureLogger struct{ buf *bytes.Buffer }

func (l *captureLogger) write(msg string, args ...interface{}) {
	l.buf.WriteString(fmt.Sprintf(msg, args...))
	l.buf.WriteString("\n")
}
func (l *captureLogger) Warnf(m string, a ...interface{})  { l.write(m, a...) }
func (l *captureLogger) Errorf(m string, a ...interface{}) { l.write(m, a...) }
func (l *captureLogger) Infof(m string, a ...interface{})  { l.write(m, a...) }
func (l *captureLogger) Debugf(m string, a ...interface{}) { l.write(m, a...) }
func (l *captureLogger) Sub(string) waLog.Logger           { return l }

func doRaw(t *testing.T, h http.Handler, method, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		r = httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func doJSON[T any](t *testing.T, h http.Handler, method, path string, body interface{}) T {
	t.Helper()
	rec := doRaw(t, h, method, path, body)
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s %s: %v (body=%s)", method, path, err, rec.Body.String())
	}
	return out
}

func containsSub(list []string, sub string) bool {
	sub = strings.ToLower(sub)
	for _, s := range list {
		if strings.Contains(strings.ToLower(s), sub) {
			return true
		}
	}
	return false
}
