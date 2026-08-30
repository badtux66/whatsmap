# WhatsMap Timing-Research Governance Console

A responsive, accessible web console for **authorized, consent-based** WhatsApp
timing research. It is a governance layer: it demonstrates and enforces the
controls that legitimate RTT research requires, and it runs on **mock data
only** — it does not link a WhatsApp account and does not drive the covert
probing engine in the `mapper` package.

## Why this exists

The underlying RTT technique can be abused to monitor people without their
knowledge. This console deliberately moves in the opposite direction:

- **No free-form targets.** Experiments can only be aimed at a participant from
  a consent-verified allowlist. Arbitrary phone-number entry is not offered.
- **Consent is a precondition.** A participant is eligible only with verified,
  unexpired consent *and* either a documented consent reference or verified
  device-ownership. Pending, expired, and revoked participants are rejected.
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
go run ./cmd/waresearch-ui -addr 127.0.0.1:8080
# then open http://127.0.0.1:8080
```

Flags: `-addr` (listen address, default `127.0.0.1:8080`) and `-log`
(`DEBUG|INFO|WARN|ERROR`).

## Layout

```
webui/
├── types.go        # DTOs, safety limits, hypothesis bands, stats helpers
├── validate.go     # Server-side config + allowlist/consent validation
├── mock.go         # Deterministic mock participants, QR placeholder, RTT generator
├── server.go       # HTTP server, QR state machine, experiment run loop, telemetry
├── webui_test.go   # Table-driven tests
└── static/         # index.html, styles.css, app.js (embedded via go:embed)
cmd/waresearch-ui/  # Entry point
```

## HTTP API (JSON)

| Method & path | Purpose |
|---|---|
| `GET /api/session` | Current QR/connection state (advances the mock state machine) |
| `POST /api/session/connect[?simulate=expired\|error]` | Begin mock pairing |
| `POST /api/session/disconnect` | Tear down the link (also stops any run) |
| `GET /api/participants` | Consent-gated allowlist with per-row `eligible` |
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

## Security notes

- A strict `Content-Security-Policy` (`default-src 'self'`, no inline script or
  external origins) is set on every response; the front-end ships no inline
  scripts.
- The QR "matrix" returned for on-screen rendering is a deterministic
  placeholder that encodes nothing. Real QR contents, credentials, and session
  tokens are never produced, returned, or logged.

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

Covers the safety-limit matrix, allowlist/consent rejection, probe estimation,
band classification, the QR state machine (including expiry/error), start
gating, the run loop with iteration/duration completion, emergency stop, the
hypothesis-framing invariants, and that pairing never logs QR/account data.
