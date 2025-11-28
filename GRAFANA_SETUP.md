# Grafana + Prometheus Integration

RollingStone now exports metrics to Prometheus for professional visualization in Grafana.

## Quick Start

### 1. Install (One-Time Setup)

```bash
brew install prometheus grafana
```

### 2. Start Everything

```bash
./start.sh
```

This automatically starts:
- Simulator (http://localhost:8080)
- Prometheus (http://localhost:9090) - Metrics storage
- Grafana (http://localhost:3000) - Dashboards

### 3. Set Up Grafana Dashboard

1. Open http://localhost:3000
2. Login: admin / admin (change password when prompted)
3. Go to **Dashboards** → **Import**
4. Upload `grafana-dashboard.json`
5. Select "Prometheus" as data source
6. Click **Import**

### 4. View Metrics

Open the "RollingStone LSM Simulator" dashboard to see:
- L0 file count over time
- Write/Read/Space amplification
- Disk utilization
- LSM size growth
- Write stall indicators

## Running Without Prometheus/Grafana

If you don't have Prometheus/Grafana installed, `./start.sh` will still work:
- Simulator runs normally at http://localhost:8080
- Metrics available at http://localhost:8080/metrics (text format)
- Web UI shows basic metrics

## Why This Solves the Charting Problem

**Browser-based charts failed at 100× speed:**
```
100× speed = 200 data points/second
After 10 minutes: 120,000 points in browser memory
Result: Memory leak, UI freeze
```

**Prometheus + Grafana handles it:**
```
Prometheus: Separate process, built for time-series
Grafana: Queries only visible timerange, auto-downsamples
Result: Smooth charts even at 1000× speed
```

## Available Metrics

```
rocksdb_l0_files                    # Number of L0 files
rocksdb_write_amplification         # Write amplification factor
rocksdb_read_amplification          # Read amplification (files checked)
rocksdb_total_size_mb               # Total LSM size in MB
rocksdb_disk_utilization_percent    # Disk utilization (0-100%)
rocksdb_is_stalled                  # Write stall state (0=normal, 1=stalled)
```

## Querying in Prometheus

Open http://localhost:9090 and try queries like:
```
rocksdb_l0_files                           # Current L0 count
rate(rocksdb_total_size_mb[1m])            # Growth rate
rocksdb_write_amplification > 5            # High write amp alert
```

## Grafana Tips

- **Auto-refresh:** Set to 5s for live updates
- **Time range:** Last 5m for recent data, Last 1h for trends
- **Annotations:** Add markers for config changes
- **Alerts:** Set thresholds (e.g., disk > 80%)

## Stopping Everything

```bash
curl http://localhost:8080/quitquitquit  # Stops all three processes
```

Or manually:
```bash
kill <PROM_PID> <GRAF_PID> <SIM_PID>
```

## Data Persistence

- **Prometheus data:** Stored in `./prom-data/` (survives restarts)
- **Grafana dashboards:** Stored in `/opt/homebrew/var/lib/grafana/`
- **Simulator data:** In-memory only (resets on restart)

To clear Prometheus history:
```bash
rm -rf prom-data/
```
