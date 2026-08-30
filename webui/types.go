// Package webui implements a consent-gated research governance dashboard for
// the WhatsMap RTT timing-research project.
//
// # Scope and intent
//
// This package is deliberately self-contained and serves *mock* data only. It
// does NOT import or drive the covert probing engine in the mapper package. Its
// purpose is the opposite of covert monitoring: it demonstrates and enforces the
// governance controls that authorized timing research requires before any live
// measurement could ever run — an allowlist of enrolled participants, verified
// consent or device-ownership, safe experiment limits, and an always-available
// emergency stop.
//
// Two framing rules are enforced throughout the API and UI:
//
//  1. Round-trip time (RTT) is treated as an indirect research signal, never as
//     proof of a device or application state. Latency bands are labelled as
//     "possible" hypotheses and always shown with confidence, uncertainty,
//     distribution overlap, and confounders.
//  2. Free-form target entry is not offered. Experiments can only target a
//     participant from the consent-verified allowlist.
package webui

import (
	"sort"
	"time"
)

// ConnectionState models the lifecycle of a QR-based research-account link.
type ConnectionState string

const (
	ConnIdle         ConnectionState = "idle"         // No link attempt yet.
	ConnPending      ConnectionState = "pending"      // QR shown, waiting for the researcher to scan.
	ConnConnected    ConnectionState = "connected"    // Research account linked.
	ConnExpired      ConnectionState = "expired"      // QR expired before it was scanned.
	ConnDisconnected ConnectionState = "disconnected" // Link was torn down.
	ConnError        ConnectionState = "error"        // Pairing failed.
)

// ExperimentStatus models the lifecycle of an experiment run.
type ExperimentStatus string

const (
	ExpIdle      ExperimentStatus = "idle"
	ExpRunning   ExperimentStatus = "running"
	ExpStopped   ExperimentStatus = "stopped"   // Halted early (including emergency stop).
	ExpCompleted ExperimentStatus = "completed" // Reached its duration or iteration limit.
	ExpError     ExperimentStatus = "error"
)

// ConsentStatus captures the enrollment/consent state of a research participant.
type ConsentStatus string

const (
	ConsentVerified ConsentStatus = "verified"
	ConsentPending  ConsentStatus = "pending"
	ConsentExpired  ConsentStatus = "expired"
	ConsentRevoked  ConsentStatus = "revoked"
)

// Safety limits. These are intentionally conservative research guardrails; they
// bound probe rate, run length, and iteration count so that even a mis-typed
// experiment configuration cannot turn into high-frequency or open-ended
// measurement. Every limit is enforced server-side in ValidateConfig.
const (
	MinIntervalMs  = 1000     // No faster than one probe per second.
	MaxIntervalMs  = 3600_000 // One hour between probes is the slowest useful cadence.
	MinDurationSec = 10       // Runs shorter than this are almost always mistakes.
	MaxDurationSec = 6 * 3600 // Hard cap of six hours per run.
	MinTimeoutMs   = 1000     // A probe must wait at least a second for a receipt.
	MaxTimeoutMs   = 60_000   // ...and no more than a minute.
	MaxProbesCap   = 20_000   // Absolute ceiling on iterations per run.
	MaxProbeRate   = 1.0      // Probes per second; mirrors MinIntervalMs.
)

// SessionStatus is the JSON shape returned for the QR connection workflow.
//
// SECURITY: QRMatrix is an obviously-fake placeholder used only for on-screen
// rendering in mock mode. Real QR contents, credentials, and session tokens are
// never placed in this struct and never written to logs.
type SessionStatus struct {
	State        ConnectionState `json:"state"`
	Account      string          `json:"account,omitempty"` // Masked label, never a raw identifier.
	ExpiresInSec int             `json:"expires_in_sec,omitempty"`
	Message      string          `json:"message,omitempty"`
	Mock         bool            `json:"mock"`
	QRMatrix     [][]bool        `json:"qr_matrix,omitempty"` // Placeholder only; see note above.
}

// Participant is a consent-gated research subject. Raw phone numbers are never
// exposed; only a display label and a masked contact are returned.
type Participant struct {
	ID                string        `json:"id"`
	Label             string        `json:"label"`
	MaskedContact     string        `json:"masked_contact"`
	ConsentStatus     ConsentStatus `json:"consent_status"`
	ConsentRef        string        `json:"consent_ref"` // Document reference, e.g. "IRB-2026-014 / form C-3".
	OwnershipVerified bool          `json:"ownership_verified"`
	ConsentExpiry     string        `json:"consent_expiry"` // ISO-8601 date.
	Enrolled          bool          `json:"enrolled"`
}

// Eligible reports whether this participant may be targeted by an experiment.
// A participant is eligible only with verified, unexpired consent AND either
// documented consent or verified device-ownership on file.
func (p Participant) Eligible(now time.Time) bool {
	if !p.Enrolled || p.ConsentStatus != ConsentVerified {
		return false
	}
	if p.ConsentRef == "" && !p.OwnershipVerified {
		return false
	}
	if p.ConsentExpiry != "" {
		if exp, err := time.Parse("2006-01-02", p.ConsentExpiry); err == nil {
			if now.After(exp) {
				return false
			}
		}
	}
	return true
}

// ApprovedTestStates enumerates the consented measurement scenarios a
// researcher may select. Each corresponds to a state the participant knowingly
// places their own device into — this is what makes the measurement consented
// rather than covert.
var ApprovedTestStates = []TestState{
	{Key: "foreground_baseline", Label: "Foreground baseline", Description: "Participant keeps the app open in the foreground."},
	{Key: "background_baseline", Label: "Background baseline", Description: "Participant leaves the app in the background, screen on."},
	{Key: "screen_off_baseline", Label: "Screen-off baseline", Description: "Participant locks the device with the screen off."},
	{Key: "network_control", Label: "Network control", Description: "Calibration run to characterise network RTT and jitter."},
}

// TestState is one approved, consented measurement scenario.
type TestState struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// IsApprovedTestState reports whether key names an approved scenario.
func IsApprovedTestState(key string) bool {
	for _, s := range ApprovedTestStates {
		if s.Key == key {
			return true
		}
	}
	return false
}

// ExperimentConfig is the researcher-supplied configuration for a run.
type ExperimentConfig struct {
	ParticipantID string `json:"participant_id"`
	DurationSec   int    `json:"duration_sec"`
	IntervalMs    int    `json:"interval_ms"`
	MaxProbes     int    `json:"max_probes"` // Iteration limit (0 = bounded only by duration).
	TimeoutMs     int    `json:"timeout_ms"`
	TestState     string `json:"test_state"`
}

// ValidateResult is returned by the validate endpoint and reused by start.
type ValidateResult struct {
	Valid                bool     `json:"valid"`
	Errors               []string `json:"errors"`
	Warnings             []string `json:"warnings"`
	EstimatedProbes      int      `json:"estimated_probes"`
	EstimatedDurationSec int      `json:"estimated_duration_sec"`
	EffectiveRate        float64  `json:"effective_rate_per_sec"`
}

// EstimateProbes returns how many probes a config would send: the smaller of the
// duration-derived count and the explicit iteration limit (when set).
func (c ExperimentConfig) EstimateProbes() int {
	if c.IntervalMs <= 0 {
		return 0
	}
	byDuration := (c.DurationSec * 1000) / c.IntervalMs
	est := byDuration
	if c.MaxProbes > 0 && c.MaxProbes < est {
		est = c.MaxProbes
	}
	if est < 0 {
		est = 0
	}
	return est
}

// ExperimentState is the JSON shape describing the current run.
type ExperimentState struct {
	Status          ExperimentStatus  `json:"status"`
	Config          *ExperimentConfig `json:"config,omitempty"`
	Participant     *Participant      `json:"participant,omitempty"`
	TestState       string            `json:"test_state,omitempty"`
	StartedAt       string            `json:"started_at,omitempty"`
	ProbesSent      int               `json:"probes_sent"`
	EstimatedProbes int               `json:"estimated_probes"`
	Message         string            `json:"message,omitempty"`
}

// Band is one latency hypothesis band. Bands are framed as possibilities, never
// as confirmed device states.
type Band struct {
	Key        string  `json:"key"`
	MinMs      float64 `json:"min_ms"`
	MaxMs      float64 `json:"max_ms"` // 0 means open-ended (>= MinMs).
	Hypothesis string  `json:"hypothesis"`
	Color      string  `json:"color"`
}

// Bands are the configurable research hypotheses. Wording is deliberately
// hedged ("possible ...") and never claims RTT proves a state.
var Bands = []Band{
	{Key: "foreground", MinMs: 0, MaxMs: 300, Hypothesis: "possible foreground activity", Color: "#2f9e6b"},
	{Key: "screen_on", MinMs: 300, MaxMs: 1000, Hypothesis: "possible screen-on / background activity", Color: "#4a90d9"},
	{Key: "screen_off", MinMs: 1000, MaxMs: 3000, Hypothesis: "possible screen-off activity", Color: "#d99a4a"},
	{Key: "doze", MinMs: 3000, MaxMs: 0, Hypothesis: "possible doze, sleep, or network delay", Color: "#c25b5b"},
}

// BandFor returns the band key an RTT falls into.
func BandFor(rttMs float64) string {
	for _, b := range Bands {
		if rttMs >= b.MinMs && (b.MaxMs == 0 || rttMs < b.MaxMs) {
			return b.Key
		}
	}
	return "doze"
}

// Sample is one RTT measurement point for the live chart.
type Sample struct {
	T       int64   `json:"t"` // Unix milliseconds.
	RTTMs   float64 `json:"rtt_ms"`
	BandKey string  `json:"band_key"`
	Success bool    `json:"success"`
}

// Confounder documents a factor that can distort the RTT signal.
type Confounder struct {
	Name   string `json:"name"`
	Impact string `json:"impact"` // "low" | "medium" | "high".
	Note   string `json:"note"`
}

// Telemetry is the live-visualization payload.
type Telemetry struct {
	Connection          ConnectionState `json:"connection"`
	Running             bool            `json:"running"`
	Samples             []Sample        `json:"samples"`
	CurrentRTTMs        *float64        `json:"current_rtt_ms"`
	MedianRTTMs         *float64        `json:"median_rtt_ms"`
	P95RTTMs            *float64        `json:"p95_rtt_ms"`
	Count               int             `json:"count"`
	Bands               []Band          `json:"bands"`
	Confidence          float64         `json:"confidence"` // 0..1 overall, grows with sample count.
	UncertaintyNote     string          `json:"uncertainty_note"`
	Distribution        map[string]int  `json:"distribution"`         // Band key -> sample count.
	Overlap             float64         `json:"distribution_overlap"` // 0..1 estimate of band overlap.
	Confounders         []Confounder    `json:"confounders"`
	VerifiedGroundTruth *string         `json:"verified_ground_truth"` // nil = not verified; RTT is indirect only.
}

// stats computes median and p95 over successful samples.
func stats(samples []Sample) (median, p95 *float64, count int) {
	var rtts []float64
	for _, s := range samples {
		if s.Success && s.RTTMs > 0 {
			rtts = append(rtts, s.RTTMs)
		}
	}
	count = len(rtts)
	if count == 0 {
		return nil, nil, 0
	}
	sort.Float64s(rtts)
	m := percentile(rtts, 50)
	p := percentile(rtts, 95)
	return &m, &p, count
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	idx := (p / 100.0) * float64(len(sorted)-1)
	lo := int(idx)
	hi := lo + 1
	if hi >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	w := idx - float64(lo)
	return sorted[lo]*(1-w) + sorted[hi]*w
}
