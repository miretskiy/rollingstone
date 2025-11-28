package main

import (
	"github.com/miretskiy/rollingstone/simulator"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	// Prometheus metrics (gauges)
	promMetrics = struct {
		writeAmp         prometheus.Gauge
		readAmp          prometheus.Gauge
		l0Files          prometheus.Gauge
		totalSizeMB      prometheus.Gauge
		isStalled        prometheus.Gauge
		diskUtil         prometheus.Gauge
		writeThroughput  prometheus.Gauge
		readThroughput   prometheus.Gauge
	}{
		writeAmp: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "rocksdb_write_amplification",
			Help: "Write amplification factor",
		}),
		readAmp: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "rocksdb_read_amplification",
			Help: "Read amplification (files checked per lookup)",
		}),
		l0Files: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "rocksdb_l0_files",
			Help: "Number of L0 files",
		}),
		totalSizeMB: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "rocksdb_total_size_mb",
			Help: "Total LSM size in MB",
		}),
		isStalled: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "rocksdb_is_stalled",
			Help: "Write stall state (0=normal, 1=stalled)",
		}),
		diskUtil: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "rocksdb_disk_utilization_percent",
			Help: "Disk utilization percentage",
		}),
		writeThroughput: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "rocksdb_write_throughput_mbps",
			Help: "Total write throughput in MB/s",
		}),
		readThroughput: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "rocksdb_read_throughput_mbps",
			Help: "Read throughput in MB/s",
		}),
	}
)

func initPrometheusMetrics() {
	prometheus.MustRegister(
		promMetrics.writeAmp,
		promMetrics.readAmp,
		promMetrics.l0Files,
		promMetrics.totalSizeMB,
		promMetrics.isStalled,
		promMetrics.diskUtil,
		promMetrics.writeThroughput,
		promMetrics.readThroughput,
	)
}

func updatePrometheusMetrics(metrics *simulator.Metrics, state *simState) {
	promMetrics.writeAmp.Set(metrics.WriteAmplification)
	promMetrics.readAmp.Set(metrics.ReadAmplification)

	// Get L0 count from state
	lsmState := state.state()
	if levels, ok := lsmState["levels"].([]interface{}); ok && len(levels) > 0 {
		if l0, ok := levels[0].(map[string]interface{}); ok {
			if count, ok := l0["fileCount"].(int); ok {
				promMetrics.l0Files.Set(float64(count))
			}
		}
	}

	if totalSize, ok := lsmState["totalSizeMB"].(float64); ok {
		promMetrics.totalSizeMB.Set(totalSize)
	}

	if metrics.IsStalled {
		promMetrics.isStalled.Set(1.0)
	} else {
		promMetrics.isStalled.Set(0.0)
	}

	promMetrics.diskUtil.Set(metrics.DiskUtilizationPercent)
	promMetrics.writeThroughput.Set(metrics.TotalWriteThroughputMBps)
	promMetrics.readThroughput.Set(metrics.ReadBandwidthMBps)
}
