# WhatsApp Mapper (wamapper)

A lower-level research tool based on the ["Careless Whisper"](https://arxiv.org/abs/2411.11194) research paper. It measures WhatsApp delivery-receipt Round-Trip Times (RTT) and derives *hypotheses* about device activity patterns.

> ⚠️ **LEGAL & ETHICAL NOTICE**: Use this tool **only** on devices you own or on
> participants who have given documented, informed consent. It accepts a raw
> phone number and performs **no consent checks of its own** — that
> responsibility is entirely yours, on every run. Measuring a non-consenting
> person is a privacy violation and is illegal in most jurisdictions. For a
> consent-gated interface with participant allowlisting and safe limits, use the
> [Research Governance Console](../../webui/README.md) instead.

## What it can suggest (hypotheses, not facts)

RTT is an indirect signal. The tool derives the following *possibilities*, each
of which should be reported with confidence and uncertainty rather than treated
as ground truth:
- **Screen on/off**: RTT < 1000 ms is *consistent with* the screen being on
- **App foreground**: RTT < 300 ms *may suggest* WhatsApp is in the foreground
- **Inactivity**: sustained high RTT *may indicate* the device is idle
- **Device type**: iOS vs Android *estimate* from RTT variance patterns
- **Connection type**: Wi-Fi vs cellular *estimate* from RTT consistency
- **Multi-device usage**: *possible* companion devices (desktop/web clients)

These are correlations observed in the research, not deterministic mappings;
network conditions and server load can produce the same timings.

## Installation

### Prerequisites

1. Go 1.21 or later
2. Python 3.8+ with matplotlib, pandas, numpy (for visualization)
3. SQLite3

### Build

```bash
cd cmd/wamapper
go build -o wamapper

# Or install globally
go install
```

### Python Dependencies

```bash
pip install -r analysis/requirements.txt
```

## Usage

### Step 1: Initial Setup (QR Code Pairing)

First, you need to link the tool to a WhatsApp account:

```bash
./wamapper -mode qr
```

Scan the QR code with WhatsApp → Linked Devices → Link a Device

### Step 2: Start Measuring (consented participant / owned device)

Before running, confirm the number belongs to a device you own or a participant
who has given documented, informed consent:

```bash
# Basic run (2-second intervals)
./wamapper -mode probe -target 14155551234

# Extended run (24 hours, considerate 30-second interval)
./wamapper -mode probe -target 14155551234 -duration 24h -interval 30s
```

Keep the interval considerate (seconds, not sub-second). Very high-frequency
probing increases load and is not appropriate for research on a shared service;
the governance console enforces a one-probe-per-second floor for this reason.

### Step 3: Analyze Results

```bash
# Text report
./wamapper -mode analyze -target 14155551234

# JSON output
./wamapper -mode analyze -target 14155551234 -json

# Export for visualization
./wamapper -mode export -target 14155551234 -export-csv data.csv
```

### Step 4: Visualize

```bash
python analysis/visualize.py data.csv -o report.png
```

## Command Line Options

| Flag | Description | Default |
|------|-------------|---------|
| `-mode` | Operating mode: qr, probe, analyze, export | probe |
| `-db` | WhatsApp session database path | mapper.db |
| `-mapper-db` | RTT measurements database path | rtt_data.db |
| `-target` | Target phone number (with country code, no +) | required |
| `-interval` | Time between probes | 2s |
| `-duration` | Total probing duration (0 = unlimited) | 0 |
| `-max-probes` | Maximum probes to send (0 = unlimited) | 0 |
| `-probe-type` | Probe method: reaction, receipt, presence | reaction |
| `-json` | Output analysis as JSON | false |
| `-export-csv` | Export to CSV file | - |
| `-log` | Log level: DEBUG, INFO, WARN, ERROR | INFO |

## RTT Interpretation (hypotheses)

Based on the Careless Whisper paper. Each row is a *possible* interpretation,
not a confirmed state — the bands overlap and network conditions can reproduce
any of these timings:

| RTT Range | Possible interpretation |
|-----------|-------------------------|
| < 300 ms | *possible* app foreground |
| 300–1000 ms | *possible* screen-on / background |
| 1000–3000 ms | *possible* screen-off |
| 3000–10000 ms | *possible* doze / power-saving |
| > 10000 ms | *possible* extended inactivity or network delay |
| Timeout | unreachable (offline or no data connection) |

**Confounders to keep in mind:** network RTT (cellular/Wi-Fi), packet loss,
messaging-server load, and connection reuse. Report interpretations with
confidence and uncertainty, and never present RTT as proof of device state.

## Probe Types

Each of these elicits a delivery receipt without notifying the measured device —
a privacy side-channel that is only appropriate with consent or on your own
device.

### Reaction
Sends a reaction to a non-existent message; the device responds with a delivery
receipt. The most reliable of the three.

### Receipt
Uses the read-receipt mechanism. Less reliable but doesn't require message creation.

### Presence
Subscribes to online/offline presence. Provides direct status but may be rate-limited.

## Database Schema

Measurements are stored in SQLite with the following structure:

```sql
rtt_measurements (
    id, target_jid, timestamp, rtt_ms, server_ack_ms, 
    device_ack_ms, probe_type, success, error_message, inferred_state
)

device_info (
    target_jid, device_count, os_type, detected_at
)

activity_patterns (
    target_jid, pattern_type, start_time, end_time, confidence, description
)
```

## Example Analysis Output

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
Device Count: 1
Companion Device Detected: No

RTT STATISTICS
──────────────
Total Measurements: 1440
Average RTT: 892.5 ms
Screen On Avg: 425.3 ms
Screen Off Avg: 2150.8 ms

DAILY PATTERN
─────────────
Typical Wake Time: 07:00
Typical Sleep Time: 23:00
Peak Activity Hours: [9, 12, 18, 21]
```

## Visualization Output

The Python visualization generates:
1. **RTT Timeline**: Scatter plot of RTT over time, colored by inferred state
2. **Activity Heatmap**: Hour-by-day heatmap showing activity levels
3. **Daily Pattern Chart**: Bar chart of hourly activity percentages
4. **State Distribution**: Pie chart of time spent in each state
5. **RTT Histogram**: Distribution of RTT values with threshold markers

## Security & Privacy Considerations

- All data is stored locally in SQLite databases; nothing is sent to external servers.
- The technique uses WhatsApp's standard delivery-receipt mechanism.
- Because the measurement is **not visible** to the device owner, it is a privacy
  side-channel: only run it with documented consent or on a device you own.
- Store measurements securely and delete them when the research no longer needs
  them; timing traces can be sensitive.

## Research Reference

This tool implements techniques described in:

> Gegenhuber, G.K., et al. "Careless Whisper: Exploiting Silent Delivery Receipts to Monitor Users on Mobile Instant Messengers." arXiv:2411.11194, 2024. **RAID 2025 Best Paper Award**.

## License

This tool is provided for research purposes only. Use responsibly and in compliance with applicable laws.

