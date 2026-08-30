# WhatsMap - WhatsApp Activity Mapper

**RTT-based device activity monitoring for authorized OSINT research**

Based on the ["Careless Whisper"](https://arxiv.org/abs/2411.11194) research paper (RAID 2025 Best Paper Award).

> ⚠️ **LEGAL DISCLAIMER**: This tool is designed for authorized penetration testing and OSINT research on company-owned devices only. Unauthorized monitoring of individuals is illegal.

---

## Overview

WhatsMap extends the [whatsmeow](https://github.com/tulir/whatsmeow) library with RTT-based activity monitoring capabilities. It uses delivery receipt timing to infer device states without alerting the target.

### Key Features

- **Stealthy Probing**: Uses reactions to non-existent messages (no notifications on target)
- **RTT Analysis**: Measures round-trip times to infer screen on/off states
- **Device Detection**: Identifies iOS vs Android based on timing patterns
- **Activity Mapping**: Generates heatmaps of device usage over time
- **Pattern Recognition**: Detects wake/sleep times, active usage periods

## Quick Start

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

### 3. Start Monitoring

```bash
# Monitor target for 24 hours with 30-second intervals
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

## Research Governance Console (Web UI)

A responsive, accessible web console for **authorized, consent-based** research
is available under [`webui/`](webui/README.md). It enforces an allowlist of
consent-verified participants, frames latency bands as hypotheses (never proof
of a device state), bounds experiment parameters with an always-available
emergency stop, and runs on **mock data only** — it does not link an account or
drive live probing.

```bash
go run ./cmd/waresearch-ui -addr 127.0.0.1:8080
# open http://127.0.0.1:8080
```

## How It Works

The tool exploits the following observation from the Careless Whisper paper:

| RTT Range | Device State | Description |
|-----------|--------------|-------------|
| < 300ms | App Foreground | WhatsApp is actively open |
| 300-1000ms | Screen On | Phone active, WA in background |
| 1000-3000ms | Screen Off | Phone screen is off |
| > 3000ms | Doze/Sleep | Power saving mode active |

### Stealthy Probing Technique

1. Send a reaction to a **non-existent message ID**
2. Target device still sends delivery receipt
3. Measure RTT from send to receipt
4. **No notification is shown to target**

## Project Structure

```
whatsmap/
├── cmd/wamapper/          # CLI tool
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
