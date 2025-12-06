#!/usr/bin/env python3
"""
WhatsApp Activity Visualization Tool
Based on "Careless Whisper" research paper (arXiv:2411.11194)

Generates heatmaps and visualizations of RTT measurements to identify:
- Screen on/off states
- Phone usage patterns
- Activity timelines
- OS type indicators

Usage:
    python visualize.py measurements.csv [--output report.png]
"""

import argparse
import sys
from datetime import datetime, timedelta
from pathlib import Path
from typing import Optional, Dict, List, Tuple

import numpy as np
import pandas as pd
import matplotlib.pyplot as plt
import matplotlib.dates as mdates
from matplotlib.colors import LinearSegmentedColormap
from matplotlib.patches import Patch
import matplotlib.gridspec as gridspec


# RTT thresholds based on the Careless Whisper paper
RTT_THRESHOLDS = {
    'app_foreground': 300,    # < 300ms: App is actively open
    'screen_on': 1000,        # < 1000ms: Screen is on
    'screen_off': 3000,       # < 3000ms: Screen is off
    'doze_mode': 10000,       # < 10000ms: Device in doze/sleep mode
    # Anything above is deep sleep or offline
}

# State colors for visualization
STATE_COLORS = {
    'app_foreground': '#00ff00',  # Bright green
    'screen_on': '#90EE90',       # Light green
    'screen_off': '#FFD700',      # Gold
    'doze_mode': '#FFA500',       # Orange
    'deep_sleep': '#FF4500',      # Red-orange
    'offline': '#8B0000',         # Dark red
}

# Custom colormap for RTT heatmap
RTT_CMAP = LinearSegmentedColormap.from_list(
    'rtt_activity',
    ['#00ff00', '#90EE90', '#FFD700', '#FFA500', '#FF4500', '#8B0000'],
    N=256
)


def load_data(csv_path: str) -> pd.DataFrame:
    """Load RTT measurements from CSV file."""
    df = pd.read_csv(csv_path)
    df['timestamp'] = pd.to_datetime(df['timestamp'])
    df['hour'] = df['timestamp'].dt.hour
    df['day_of_week'] = df['timestamp'].dt.dayofweek
    df['date'] = df['timestamp'].dt.date
    return df


def classify_state(rtt_ms: float, success: bool) -> str:
    """Classify device state based on RTT."""
    if not success or pd.isna(rtt_ms):
        return 'offline'
    if rtt_ms < RTT_THRESHOLDS['app_foreground']:
        return 'app_foreground'
    elif rtt_ms < RTT_THRESHOLDS['screen_on']:
        return 'screen_on'
    elif rtt_ms < RTT_THRESHOLDS['screen_off']:
        return 'screen_off'
    elif rtt_ms < RTT_THRESHOLDS['doze_mode']:
        return 'doze_mode'
    else:
        return 'deep_sleep'


def detect_os_type(df: pd.DataFrame) -> Tuple[str, float]:
    """
    Detect OS type (iOS vs Android) based on RTT patterns.
    
    iOS tends to have more consistent RTTs with lower variance.
    Android has more variation due to doze mode and OEM implementations.
    """
    successful = df[df['success'] == True]['rtt_ms'].dropna()
    
    if len(successful) < 20:
        return 'unknown', 0.5
    
    # Calculate coefficient of variation
    mean_rtt = successful.mean()
    std_rtt = successful.std()
    cv = std_rtt / mean_rtt if mean_rtt > 0 else 1
    
    # iOS typically has lower CV
    if cv < 0.3:
        return 'ios', min(0.7 + (0.3 - cv), 0.95)
    elif cv > 0.5:
        return 'android', min(0.6 + (cv - 0.5) * 0.5, 0.90)
    else:
        return 'unknown', 0.5


def create_timeline_plot(ax, df: pd.DataFrame):
    """Create a timeline plot showing RTT over time with state colors."""
    successful = df[df['success'] == True].copy()
    
    if len(successful) == 0:
        ax.text(0.5, 0.5, 'No successful measurements', 
                ha='center', va='center', transform=ax.transAxes)
        return
    
    # Color by state
    successful['state'] = successful['rtt_ms'].apply(lambda x: classify_state(x, True))
    colors = [STATE_COLORS[s] for s in successful['state']]
    
    ax.scatter(successful['timestamp'], successful['rtt_ms'], 
               c=colors, s=10, alpha=0.6)
    
    # Add threshold lines
    for name, threshold in RTT_THRESHOLDS.items():
        if threshold < successful['rtt_ms'].max() * 1.5:
            ax.axhline(y=threshold, color=STATE_COLORS[name], 
                      linestyle='--', alpha=0.5, linewidth=1)
            ax.text(successful['timestamp'].max(), threshold, 
                   f'  {name}', va='bottom', fontsize=8, color=STATE_COLORS[name])
    
    ax.set_xlabel('Time')
    ax.set_ylabel('RTT (ms)')
    ax.set_title('RTT Timeline - Device Activity')
    ax.set_yscale('log')
    ax.xaxis.set_major_formatter(mdates.DateFormatter('%m/%d %H:%M'))
    ax.tick_params(axis='x', rotation=45)
    
    # Legend
    legend_elements = [Patch(facecolor=color, label=state) 
                       for state, color in STATE_COLORS.items()]
    ax.legend(handles=legend_elements, loc='upper right', fontsize=8)


def create_hourly_heatmap(ax, df: pd.DataFrame):
    """Create a heatmap showing activity by hour and day."""
    successful = df[df['success'] == True].copy()
    
    if len(successful) == 0:
        ax.text(0.5, 0.5, 'No successful measurements', 
                ha='center', va='center', transform=ax.transAxes)
        return
    
    # Create pivot table: days vs hours
    successful['day'] = successful['timestamp'].dt.date
    pivot = successful.pivot_table(
        values='rtt_ms', 
        index='day', 
        columns='hour', 
        aggfunc='mean'
    )
    
    # Fill missing hours with NaN
    all_hours = range(24)
    for h in all_hours:
        if h not in pivot.columns:
            pivot[h] = np.nan
    pivot = pivot.reindex(columns=sorted(pivot.columns))
    
    # Plot heatmap (inverse: low RTT = high activity = dark color)
    # Use log scale for better visualization
    log_data = np.log10(pivot.values + 1)
    
    im = ax.imshow(log_data, aspect='auto', cmap=RTT_CMAP,
                   extent=[0, 24, len(pivot.index), 0])
    
    ax.set_xlabel('Hour of Day')
    ax.set_ylabel('Date')
    ax.set_title('Activity Heatmap (Green = Active, Red = Inactive)')
    ax.set_xticks(range(0, 24, 3))
    ax.set_xticklabels([f'{h:02d}:00' for h in range(0, 24, 3)])
    
    # Set y-axis labels to dates
    y_ticks = range(0, len(pivot.index), max(1, len(pivot.index) // 10))
    ax.set_yticks(list(y_ticks))
    ax.set_yticklabels([str(pivot.index[i]) for i in y_ticks], fontsize=8)
    
    plt.colorbar(im, ax=ax, label='log₁₀(RTT) - Lower = More Active')


def create_daily_pattern_chart(ax, df: pd.DataFrame):
    """Create a bar chart showing activity pattern by hour."""
    successful = df[df['success'] == True].copy()
    
    if len(successful) == 0:
        return
    
    # Calculate percentage of "screen on" time per hour
    hourly_stats = []
    for hour in range(24):
        hour_data = successful[successful['hour'] == hour]
        if len(hour_data) > 0:
            screen_on_pct = (hour_data['rtt_ms'] < RTT_THRESHOLDS['screen_on']).mean() * 100
            active_pct = (hour_data['rtt_ms'] < RTT_THRESHOLDS['app_foreground']).mean() * 100
            avg_rtt = hour_data['rtt_ms'].mean()
        else:
            screen_on_pct = 0
            active_pct = 0
            avg_rtt = 0
        hourly_stats.append({
            'hour': hour,
            'screen_on_pct': screen_on_pct,
            'active_pct': active_pct,
            'avg_rtt': avg_rtt
        })
    
    stats_df = pd.DataFrame(hourly_stats)
    
    # Bar chart
    bars = ax.bar(stats_df['hour'], stats_df['screen_on_pct'], 
                  color=STATE_COLORS['screen_on'], label='Screen On %', alpha=0.7)
    ax.bar(stats_df['hour'], stats_df['active_pct'], 
           color=STATE_COLORS['app_foreground'], label='App Active %', alpha=0.9)
    
    ax.set_xlabel('Hour of Day')
    ax.set_ylabel('Percentage (%)')
    ax.set_title('Daily Activity Pattern')
    ax.set_xticks(range(0, 24, 2))
    ax.legend()
    ax.set_xlim(-0.5, 23.5)


def create_state_distribution_pie(ax, df: pd.DataFrame):
    """Create a pie chart showing distribution of device states."""
    df_copy = df.copy()
    df_copy['state'] = df_copy.apply(
        lambda r: classify_state(r['rtt_ms'], r['success']), axis=1
    )
    
    state_counts = df_copy['state'].value_counts()
    colors = [STATE_COLORS[s] for s in state_counts.index]
    
    ax.pie(state_counts.values, labels=state_counts.index, colors=colors,
           autopct='%1.1f%%', startangle=90)
    ax.set_title('Device State Distribution')


def create_rtt_histogram(ax, df: pd.DataFrame):
    """Create a histogram of RTT distribution."""
    successful = df[df['success'] == True]['rtt_ms'].dropna()
    
    if len(successful) == 0:
        return
    
    # Use log bins for better visualization
    log_bins = np.logspace(np.log10(max(1, successful.min())), 
                           np.log10(successful.max()), 50)
    
    ax.hist(successful, bins=log_bins, color='steelblue', alpha=0.7, edgecolor='black')
    
    # Add threshold lines
    for name, threshold in RTT_THRESHOLDS.items():
        ax.axvline(x=threshold, color=STATE_COLORS[name], 
                   linestyle='--', linewidth=2, label=f'{name} ({threshold}ms)')
    
    ax.set_xscale('log')
    ax.set_xlabel('RTT (ms)')
    ax.set_ylabel('Count')
    ax.set_title('RTT Distribution')
    ax.legend(fontsize=8)


def create_statistics_panel(ax, df: pd.DataFrame, os_type: str, os_confidence: float):
    """Create a text panel with statistics."""
    successful = df[df['success'] == True]
    
    stats_text = f"""
═══════════════════════════════════════════════
                 ANALYSIS SUMMARY
═══════════════════════════════════════════════

📊 MEASUREMENT STATISTICS
   Total Measurements: {len(df):,}
   Successful: {len(successful):,} ({len(successful)/len(df)*100:.1f}%)
   
📱 DEVICE DETECTION
   Likely OS: {os_type.upper()} ({os_confidence*100:.0f}% confidence)

⏱️ RTT STATISTICS
   Mean RTT: {successful['rtt_ms'].mean():.1f} ms
   Median RTT: {successful['rtt_ms'].median():.1f} ms
   Std Dev: {successful['rtt_ms'].std():.1f} ms
   Min RTT: {successful['rtt_ms'].min():.1f} ms
   Max RTT: {successful['rtt_ms'].max():.1f} ms

📅 TIME RANGE
   Start: {df['timestamp'].min().strftime('%Y-%m-%d %H:%M')}
   End: {df['timestamp'].max().strftime('%Y-%m-%d %H:%M')}
   Duration: {(df['timestamp'].max() - df['timestamp'].min())}

📊 STATE BREAKDOWN
"""
    
    df_copy = df.copy()
    df_copy['state'] = df_copy.apply(
        lambda r: classify_state(r['rtt_ms'], r['success']), axis=1
    )
    state_pcts = df_copy['state'].value_counts(normalize=True) * 100
    
    for state, pct in state_pcts.items():
        stats_text += f"   {state}: {pct:.1f}%\n"
    
    stats_text += """
═══════════════════════════════════════════════
Based on "Careless Whisper" research
(arXiv:2411.11194)
═══════════════════════════════════════════════
"""
    
    ax.text(0.5, 0.5, stats_text, transform=ax.transAxes, 
            fontsize=10, verticalalignment='center', horizontalalignment='center',
            fontfamily='monospace', bbox=dict(boxstyle='round', facecolor='wheat', alpha=0.5))
    ax.axis('off')


def generate_report(csv_path: str, output_path: Optional[str] = None):
    """Generate a comprehensive visualization report."""
    print(f"Loading data from {csv_path}...")
    df = load_data(csv_path)
    
    print(f"Loaded {len(df)} measurements")
    print(f"Date range: {df['timestamp'].min()} to {df['timestamp'].max()}")
    
    # Detect OS type
    os_type, os_confidence = detect_os_type(df)
    print(f"Detected OS: {os_type} ({os_confidence*100:.0f}% confidence)")
    
    # Create figure with subplots
    fig = plt.figure(figsize=(20, 16))
    fig.suptitle('WhatsApp Activity Analysis Report', fontsize=16, fontweight='bold')
    
    gs = gridspec.GridSpec(3, 3, figure=fig, height_ratios=[1, 1, 1])
    
    # Row 1: Timeline and Heatmap
    ax_timeline = fig.add_subplot(gs[0, :2])
    ax_stats = fig.add_subplot(gs[0, 2])
    
    # Row 2: Daily pattern and state distribution
    ax_heatmap = fig.add_subplot(gs[1, :2])
    ax_pie = fig.add_subplot(gs[1, 2])
    
    # Row 3: Histogram and daily pattern
    ax_daily = fig.add_subplot(gs[2, :2])
    ax_hist = fig.add_subplot(gs[2, 2])
    
    # Create each visualization
    print("Creating timeline plot...")
    create_timeline_plot(ax_timeline, df)
    
    print("Creating statistics panel...")
    create_statistics_panel(ax_stats, df, os_type, os_confidence)
    
    print("Creating hourly heatmap...")
    create_hourly_heatmap(ax_heatmap, df)
    
    print("Creating state distribution pie chart...")
    create_state_distribution_pie(ax_pie, df)
    
    print("Creating daily pattern chart...")
    create_daily_pattern_chart(ax_daily, df)
    
    print("Creating RTT histogram...")
    create_rtt_histogram(ax_hist, df)
    
    plt.tight_layout()
    
    # Save or show
    if output_path:
        plt.savefig(output_path, dpi=150, bbox_inches='tight')
        print(f"Report saved to {output_path}")
    else:
        plt.show()
    
    return df


def create_animated_timeline(df: pd.DataFrame, output_path: str):
    """Create an animated GIF showing activity over time (optional)."""
    try:
        from matplotlib.animation import FuncAnimation, PillowWriter
    except ImportError:
        print("Animation requires pillow: pip install pillow")
        return
    
    # Implementation for animated visualization
    # (Simplified - full implementation would be more complex)
    pass


def main():
    parser = argparse.ArgumentParser(
        description='WhatsApp Activity Visualization Tool',
        epilog='Based on "Careless Whisper" research (arXiv:2411.11194)'
    )
    parser.add_argument('csv_file', help='Path to CSV file with RTT measurements')
    parser.add_argument('-o', '--output', help='Output image file path (PNG, PDF, etc.)')
    parser.add_argument('--animate', action='store_true', help='Create animated GIF')
    
    args = parser.parse_args()
    
    if not Path(args.csv_file).exists():
        print(f"Error: File not found: {args.csv_file}")
        sys.exit(1)
    
    try:
        generate_report(args.csv_file, args.output)
    except Exception as e:
        print(f"Error generating report: {e}")
        import traceback
        traceback.print_exc()
        sys.exit(1)


if __name__ == '__main__':
    main()

