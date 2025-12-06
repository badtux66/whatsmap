# WhatsApp Mapper (wamapper)

A tool for authorized OSINT research based on the ["Careless Whisper"](https://arxiv.org/abs/2411.11194) research paper. This tool measures WhatsApp delivery receipt Round-Trip Times (RTT) to infer device activity patterns.

> ⚠️ **LEGAL DISCLAIMER**: This tool is designed for authorized penetration testing and OSINT research on company-owned devices only. Unauthorized monitoring of individuals is illegal in most jurisdictions.

## Features

Based on the research paper findings, this tool can infer:
- **Screen On/Off State**: RTT < 1000ms typically indicates screen is on
- **App Foreground**: RTT < 300ms suggests WhatsApp is actively open
- **Sleep Patterns**: Consistent high RTT periods indicate phone inactivity
- **Device Type**: iOS vs Android based on RTT variance patterns
- **Connection Type**: WiFi vs Cellular estimation from RTT consistency
- **Multi-device Usage**: Detection of companion devices (desktop/web clients)

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

### Step 2: Start Probing

Probe a target phone number (must be a valid WhatsApp user):

```bash
# Basic probe (2-second intervals)
./wamapper -mode probe -target 14155551234

# Extended monitoring (24 hours, 30-second intervals)
./wamapper -mode probe -target 14155551234 -duration 24h -interval 30s

# High-frequency probing (for detailed activity capture)
./wamapper -mode probe -target 14155551234 -interval 500ms -duration 1h
```

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

## RTT Interpretation

Based on the Careless Whisper paper:

| RTT Range | Inferred State | Description |
|-----------|----------------|-------------|
| < 300ms | App Foreground | WhatsApp is actively being used |
| 300-1000ms | Screen On | Phone is active, WA in background |
| 1000-3000ms | Screen Off | Phone screen is off |
| 3000-10000ms | Doze Mode | Power saving mode active |
| > 10000ms | Deep Sleep | Extended inactivity |
| Timeout | Offline | No data connection |

## Probe Types

### Reaction (Recommended)
Sends reactions to non-existent messages. The target device responds with a delivery receipt but shows no notification.

### Receipt
Uses read receipt mechanism. Less reliable but doesn't require message creation.

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

## Security Considerations

- All data is stored locally in SQLite databases
- No data is transmitted to external servers
- The probing technique uses WhatsApp's standard delivery receipt mechanism
- Reactions to non-existent messages do not trigger notifications on the target device

## Research Reference

This tool implements techniques described in:

> Gegenhuber, G.K., et al. "Careless Whisper: Exploiting Silent Delivery Receipts to Monitor Users on Mobile Instant Messengers." arXiv:2411.11194, 2024. **RAID 2025 Best Paper Award**.

## License

This tool is provided for research purposes only. Use responsibly and in compliance with applicable laws.

