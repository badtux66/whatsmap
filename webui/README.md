# WhatsMap Timing-Research Governance Console

A responsive, accessible web console for **authorized, consent-based** WhatsApp
timing research. It is a governance layer: it demonstrates and enforces the
controls that legitimate RTT research requires. Experiment telemetry is **mock**
(the covert probing engine in the `mapper` package is not wired in); the QR
login is mock by default and can optionally link the researcher's **own**
account for real (`-live`).

## Why this exists

The underlying RTT technique can be abused to monitor people without their
knowledge. This console deliberately moves in the opposite direction:

- **Consent is the gate, not the phone number.** You can enroll *any* number,
  but only after attesting to device ownership or documented consent (with a
  reference). Until then it cannot be targeted — there is no un-attested,
  free-form targeting.
- **Eligibility is enforced server-side.** A participant is eligible only with
  verified, unexpired consent *and* either a documented consent reference or
  verified device-ownership. Pending, expired, and revoked participants are
  rejected.
- **RTT is a hypothesis, not proof.** Latency bands are labelled "possible …"
  and always shown alongside confidence, uncertainty, distribution overlap, and
  confounders (network RTT, packet loss, server load, connection reuse).
  Measured latency is kept visually and textually distinct from verified
  ground-truth state — of which there is none in this build.
- **Safe by construction.** Probe interval, duration, iteration count, and
  timeout are bounded server-side; an emergency stop is always available; and
  the estimated probe count is shown before a run can start.

## Run it

```bash
# Mock QR login (default) — nothing links, telemetry is synthetic
go run ./cmd/waresearch-ui -addr 127.0.0.1:8080

# Real QR login for YOUR OWN account (WhatsApp → Linked devices)
go run ./cmd/waresearch-ui -addr 127.0.0.1:8080 -live -session-db waresearch.db
# then open http://127.0.0.1:8080
```

Flags: `-addr` (listen address, default `127.0.0.1:8080`), `-log`
(`DEBUG|INFO|WARN|ERROR`), `-live` (real account linking), and `-session-db`
(linked-account session store path, used with `-live`).

`-live` links only the researcher's own account so a scannable code can be
shown; it starts no probing. Experiment telemetry remains mock either way, and
who may be measured is always gated by the consent allowlist.

## Layout

```
webui/
├── types.go        # DTOs, safety limits, hypothesis bands, stats helpers
├── validate.go     # Server-side config + allowlist/consent validation
├── mock.go         # Deterministic mock participants, QR placeholder, RTT generator
├── linker.go       # SessionLinker interface + default mock QR linker
├── server.go       # HTTP server, enrollment, experiment run loop, telemetry
├── webui_test.go   # Table-driven tests
├── static/         # index.html, styles.css, app.js (embedded via go:embed)
└── live/           # Real WhatsApp linked-device QR linker (opt-in via -live)
cmd/waresearch-ui/  # Entry point
```

## HTTP API (JSON)

| Method & path | Purpose |
|---|---|
| `GET /api/session` | Current QR/connection state |
| `POST /api/session/connect[?simulate=expired\|error]` | Begin pairing (`simulate` only affects mock) |
| `POST /api/session/disconnect` | Tear down the link (also stops any run) |
| `GET /api/participants` | Consent-gated allowlist with per-row `eligible` |
| `POST /api/participants` | Enroll any number behind an ownership/consent attestation |
| `GET /api/test-states` | Approved, consented measurement scenarios |
| `POST /api/experiment/validate` | Validate a config; returns errors, warnings, probe estimate |
| `POST /api/experiment/start` | Start a run (requires connection + valid, eligible config) |
| `POST /api/experiment/stop` | Emergency stop |
| `GET /api/experiment` | Current run state |
| `GET /api/telemetry` | Live samples, rolling median/p95, bands, confidence, confounders |

### Response codes on start

- `412 Precondition Failed` — no research account linked.
- `409 Conflict` — a run is already in progress.
- `422 Unprocessable Entity` — config failed validation (unsafe limits, or a
  non-consenting / unknown participant). The body is the `ValidateResult`.
  Enrollment (`POST /api/participants`) also returns `422` (with an `errors`
  array) when the attestation, number, or reference is missing.

## Security notes

- A strict `Content-Security-Policy` (`default-src 'self'`, no inline script or
  external origins) is set on every response; the front-end ships no inline
  scripts.
- In mock mode the QR matrix is a deterministic placeholder that encodes
  nothing. In `-live` mode it encodes the real pairing code so it can be
  scanned — but the raw code string and any session token are never logged, and
  the linked account is shown only as a masked JID.
- Enrolled phone numbers are stored server-side and never returned to clients;
  only a masked contact (e.g. `+1415•••••88`) is exposed.

## Accessibility

Semantic landmarks and headings, a skip link, `radiogroup` semantics for the
participant selector, labelled form fields with `aria-invalid` on errors, live
regions for status and validation, a `progressbar` for confidence, visible
focus styles, keyboard operability throughout, and `prefers-reduced-motion` and
light/dark (`prefers-color-scheme`) support.

## Tests

```bash
go test ./webui/...
```

Covers the safety-limit matrix, allowlist/consent rejection, the enrollment
attestation gate and contact masking, probe estimation, band classification,
the QR state machine (including expiry/error), start gating, the run loop with
iteration/duration completion, emergency stop, the hypothesis-framing
invariants, and that neither pairing nor enrollment logs the QR code, account,
or raw phone number.
