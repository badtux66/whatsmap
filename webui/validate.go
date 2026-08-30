package webui

import (
	"fmt"
	"time"
)

// ValidateConfig checks an experiment configuration against the safety limits
// and the consent allowlist. It returns a ValidateResult that is safe to show
// to the researcher: it enumerates every problem rather than failing on the
// first, and always reports the estimated probe count so the researcher can see
// the blast radius before starting.
//
// participant may be nil when the participant id does not resolve; the function
// treats that as an eligibility failure rather than panicking.
func ValidateConfig(c ExperimentConfig, participant *Participant, now time.Time) ValidateResult {
	res := ValidateResult{Valid: true}

	// --- Authorized-participant enforcement ---------------------------------
	if c.ParticipantID == "" {
		res.addError("Select a research participant from the allowlist.")
	} else if participant == nil {
		res.addError("Unknown participant: only enrolled, allowlisted participants can be targeted.")
	} else if !participant.Eligible(now) {
		switch participant.ConsentStatus {
		case ConsentExpired:
			res.addError(fmt.Sprintf("Consent for %q has expired (%s). Re-verify before running experiments.", participant.Label, participant.ConsentExpiry))
		case ConsentRevoked:
			res.addError(fmt.Sprintf("Consent for %q has been revoked. This participant cannot be targeted.", participant.Label))
		case ConsentPending:
			res.addError(fmt.Sprintf("Consent for %q is still pending verification.", participant.Label))
		default:
			res.addError(fmt.Sprintf("%q is not eligible: verified consent or documented device-ownership is required.", participant.Label))
		}
	}

	// --- Approved test state ------------------------------------------------
	if c.TestState == "" {
		res.addError("Select an approved test state for this run.")
	} else if !IsApprovedTestState(c.TestState) {
		res.addError(fmt.Sprintf("%q is not an approved test state.", c.TestState))
	}

	// --- Probe interval (rate limit) ---------------------------------------
	switch {
	case c.IntervalMs <= 0:
		res.addError("Probe interval is required.")
	case c.IntervalMs < MinIntervalMs:
		res.addError(fmt.Sprintf("Probe interval must be at least %d ms (max %.0f probe/s).", MinIntervalMs, MaxProbeRate))
	case c.IntervalMs > MaxIntervalMs:
		res.addError(fmt.Sprintf("Probe interval must be at most %d ms.", MaxIntervalMs))
	}

	// --- Duration ----------------------------------------------------------
	switch {
	case c.DurationSec <= 0:
		res.addError("Duration is required.")
	case c.DurationSec < MinDurationSec:
		res.addError(fmt.Sprintf("Duration must be at least %d s.", MinDurationSec))
	case c.DurationSec > MaxDurationSec:
		res.addError(fmt.Sprintf("Duration must be at most %d s (%d h).", MaxDurationSec, MaxDurationSec/3600))
	}

	// --- Timeout -----------------------------------------------------------
	switch {
	case c.TimeoutMs <= 0:
		res.addError("Probe timeout is required.")
	case c.TimeoutMs < MinTimeoutMs:
		res.addError(fmt.Sprintf("Timeout must be at least %d ms.", MinTimeoutMs))
	case c.TimeoutMs > MaxTimeoutMs:
		res.addError(fmt.Sprintf("Timeout must be at most %d ms.", MaxTimeoutMs))
	}

	// --- Iteration limit ---------------------------------------------------
	if c.MaxProbes < 0 {
		res.addError("Iteration limit cannot be negative.")
	} else if c.MaxProbes > MaxProbesCap {
		res.addError(fmt.Sprintf("Iteration limit must be at most %d.", MaxProbesCap))
	}

	// --- Derived estimates and soft warnings -------------------------------
	if c.IntervalMs > 0 {
		res.EffectiveRate = 1000.0 / float64(c.IntervalMs)
		res.EstimatedProbes = c.EstimateProbes()
		if res.EstimatedProbes > 0 {
			res.EstimatedDurationSec = res.EstimatedProbes * c.IntervalMs / 1000
		}
		if res.EstimatedProbes > MaxProbesCap {
			res.addError(fmt.Sprintf("This configuration would send %d probes, above the %d cap. Shorten the duration or lower the interval.", res.EstimatedProbes, MaxProbesCap))
		}
		if c.TimeoutMs > 0 && c.TimeoutMs >= c.IntervalMs {
			res.addWarning("Timeout is greater than or equal to the interval; receipts may overlap between probes.")
		}
	}

	return res
}

func (r *ValidateResult) addError(msg string) {
	r.Errors = append(r.Errors, msg)
	r.Valid = false
}

func (r *ValidateResult) addWarning(msg string) {
	r.Warnings = append(r.Warnings, msg)
}
