# WhatsMap - WhatsApp Timing-Research Toolkit

**RTT timing measurement for authorized, consent-based research**

Based on the ["Careless Whisper"](https://arxiv.org/abs/2411.11194) research paper (RAID 2025 Best Paper Award).

> ⚠️ **LEGAL & ETHICAL NOTICE**: This project studies a real privacy
> side-channel. It is intended **only** for authorized research measuring
> devices whose owners have given **documented, informed consent**, or devices
> you own and control. Measuring a person who has not consented is a privacy
> violation and is illegal in most jurisdictions. Round-trip time is an
> *indirect* signal: it never proves what a device or its user is doing.

---

## Overview

WhatsMap extends the [whatsmeow](https://github.com/tulir/whatsmeow) library
with tooling to measure WhatsApp delivery-receipt round-trip times (RTT) and to
study how those timings *may* relate to device activity. RTT is treated as a
research signal to be interpreted with uncertainty — not as a monitoring oracle.

The recommended way to use the toolkit is the
[Research Governance Console](webui/README.md), which enforces consent-verified
participant allowlisting, safe experiment limits, and hypothesis-framed results.

### Key Features

- **Consent-gated workflow**: the governance console restricts measurement to an
  allowlist of participants with verified consent or documented device-ownership.
- **RTT measurement**: records delivery-receipt round-trip times and their
  distribution (median, p95, spread).
- **Hypothesis-based interpretation**: maps RTT into *possible* activity bands,
  always shown with confidence, uncertainty, and confounders — never as proof.
- **Distribution & pattern analysis**: aggregates timings into daily/hourly
  summaries for research review.
- **Local-only storage**: measurements stay in a local SQLite database.

> **A note on the probing mechanism.** The underlying technique elicits a
> delivery receipt without showing a notification to the measured device. That
> property is exactly what makes the side-channel a privacy risk, and it is why
> consent and authorization are mandatory here rather than optional.

## Quick Start (recommended): Research Governance Console

A responsive, accessible web console for **authorized, consent-based** research
lives under [`webui/`](webui/README.md). It gates targeting behind an
ownership/consent attestation (any number can be enrolled, but only once
attested), frames latency bands as hypotheses (never proof of a device state),
and bounds experiment parameters with safe minimums and an always-available
emergency stop. Experiment telemetry is **mock** — the covert probing engine is
not wired in. By default QR login links the researcher's own account via the
official WhatsApp linked-device flow; `-mock` gives an offline placeholder for
UI work.

```bash
go run ./cmd/waresearch-ui -addr 127.0.0.1:8080            # real self-account QR (default)
go run ./cmd/waresearch-ui -addr 127.0.0.1:8080 -mock      # offline placeholder QR
# open http://127.0.0.1:8080
```

Start here: the console is the interface designed to keep research within its
authorized, consented scope.

## Low-level CLI (`wamapper`)

The `wamapper` command is a lower-level research tool. Because it accepts a raw
phone number, it performs **no consent checks of its own** — you are responsible
for confirming, before every run, that the number belongs to a device you own or
to a participant who has given documented, informed consent. Prefer the
governance console for anything beyond local self-testing.

### 1. Build the Tool

```bash
cd cmd/wamapper
go build -o wamapper
```

### 2. Link WhatsApp Account

```bash
./wamapper -mode qr
# Scan QR with WhatsApp → Linked Devices
```

### 3. Measure a Consented Participant

```bash
# Measure a device you own / a consented participant for 24h at a
# considerate 30-second interval
./wamapper -mode probe -target 14155551234 -duration 24h -interval 30s
```

### 4. Analyze Results

```bash
./wamapper -mode analyze -target 14155551234
./wamapper -mode export -target 14155551234 -export-csv data.csv
```

### 5. Visualize

```bash
pip install -r analysis/requirements.txt
python analysis/visualize.py data.csv -o report.png
```

## How It Works

The toolkit builds on the following observation from the Careless Whisper
paper. The RTT bands below are **research hypotheses**, not confirmed states: a
measured RTT is consistent with the interpretation, but never proves it.

| RTT Range | Possible interpretation (hypothesis) |
|-----------|--------------------------------------|
| < 300 ms | *possible* foreground activity |
| 300–1000 ms | *possible* screen-on / background activity |
| 1000–3000 ms | *possible* screen-off activity |
| > 3000 ms | *possible* doze, sleep, or network delay |

**Confounders.** Network RTT (cellular/Wi-Fi), packet loss, messaging-server
load, and connection reuse all shift the measured value, and the bands overlap.
Any interpretation should be reported with confidence and uncertainty, and kept
distinct from verified ground-truth state (which RTT alone cannot establish).

### Probing mechanism (a privacy side-channel)

1. Send a reaction to a **non-existent message ID**.
2. The receiving device still returns a delivery receipt.
3. RTT is measured from send to receipt.
4. No notification is shown to the measured device.

Step 4 is why this is a *privacy side-channel* and why the toolkit is
consent-gated: because the measurement is not visible to the device owner, it
must only ever be run against a device you own or a participant who has
consented. It is not a feature to be used against non-consenting people.

## Project Structure

```
whatsmap/
├── webui/                 # Research governance console (recommended UI)
│   ├── server.go          # HTTP server, consent/limit enforcement, telemetry
│   ├── validate.go        # Server-side safety + consent validation
│   ├── static/            # Accessible, theme-aware dashboard (mock data)
│   └── README.md          # Console documentation
├── cmd/waresearch-ui/     # Governance console entry point
├── cmd/wamapper/          # Lower-level CLI tool
│   ├── main.go            # Main entry point
│   └── README.md          # Detailed usage guide
├── mapper/                # Core mapping functionality
│   ├── store.go           # SQLite data storage
│   ├── prober.go          # RTT probing logic
│   └── analyzer.go        # Pattern analysis
├── analysis/              # Python visualization
│   ├── visualize.py       # Matplotlib report generator
│   └── requirements.txt   # Python dependencies
└── [whatsmeow core...]    # Original whatsmeow library
```

## Example Output

### Analysis Report

```
═══════════════════════════════════════════════════
WhatsApp Activity Analysis Report
Target: 14155551234@s.whatsapp.net
Period: 2024-01-15 08:00 to 2024-01-16 08:00
Overall Confidence: 85%
═══════════════════════════════════════════════════

DEVICE ANALYSIS
───────────────
Likely OS: iOS (75% confidence)
Companion Device: No

DAILY PATTERN
─────────────
Typical Wake Time: 07:00
Typical Sleep Time: 23:00

HOURLY ACTIVITY:
  00:00 | ██ 10%
  01:00 | █ 5%
  ...
  09:00 | ███████████ 55%
  12:00 | ████████████████ 80%
  ...
```

### Visualization

The Python script generates:
- RTT timeline with state colors
- Activity heatmap (hour × day)
- Daily pattern bar charts
- State distribution pie chart
- RTT histogram

## Research Reference

This implementation is based on:

> **Careless Whisper: Exploiting Silent Delivery Receipts to Monitor Users on Mobile Instant Messengers**
> 
> Gabriel K. Gegenhuber, et al.
> 
> 28th International Symposium on Research in Attacks, Intrusions and Defenses (RAID 2025)
> 
> 🏆 **Distinguished with Best Paper Award**
> 
> [arXiv:2411.11194](https://arxiv.org/abs/2411.11194)

---

## Original whatsmeow Documentation

This project is built on top of whatsmeow, a Go library for the WhatsApp web multidevice API.

### Discussion
Matrix room: [#whatsmeow:maunium.net](https://matrix.to/#/#whatsmeow:maunium.net)

For questions about the WhatsApp protocol, use the [WhatsApp protocol Q&A](https://github.com/tulir/whatsmeow/discussions/categories/whatsapp-protocol-q-a) section on GitHub.

### Core whatsmeow Features

* Sending messages to private chats and groups (both text and media)
* Receiving all messages
* Managing groups and receiving group change events
* Joining via invite messages, using and creating invite links
* Sending and receiving typing notifications
* Sending and receiving delivery and read receipts
* Reading and writing app state (contact list, chat pin/mute status, etc)
* Sending and handling retry receipts if message decryption fails
* Sending status messages (experimental)

Things that are not yet implemented:
* Sending broadcast list messages (not supported on WhatsApp web either)
* Calls
