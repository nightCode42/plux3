// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package observability

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds the process's metrics, exactly those catalogued in
// Appendix G.1 that this phase produces (OBS-002). Each field is
// unexported so that the only way to record a value is a method here,
// which fixes the label set and keeps cardinality bounded.
type Metrics struct {
	registry *prometheus.Registry

	rpcRequests      *prometheus.CounterVec
	rpcDuration      *prometheus.HistogramVec
	manifestRequests *prometheus.CounterVec
	deltaBytes       *prometheus.HistogramVec
	deltaGeneration  *prometheus.HistogramVec
	publishJobs      *prometheus.CounterVec
	publishDuration  *prometheus.HistogramVec
	jobsQueueDepth   *prometheus.GaugeVec
}

// NewMetrics builds the registry with the process collectors and the
// Plux metrics of Appendix G.1.
func NewMetrics() *Metrics {
	r := prometheus.NewRegistry()
	r.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	m := &Metrics{
		registry: r,
		rpcRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "plux_rpc_requests_total",
			Help: "RPC requests by service, method and result code.",
		}, []string{"service", "method", "code"}),
		rpcDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "plux_rpc_duration_seconds",
			Help:    "RPC duration by service and method.",
			Buckets: prometheus.DefBuckets,
		}, []string{"service", "method"}),
		manifestRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "plux_manifest_requests_total",
			Help: "Manifest requests by environment and result.",
		}, []string{"environment", "result"}),
		deltaBytes: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "plux_delta_bytes",
			Help:    "Bytes a device is asked to download, by kind.",
			Buckets: prometheus.ExponentialBuckets(1024, 4, 10),
		}, []string{"kind"}),
		deltaGeneration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "plux_delta_generation_seconds",
			Help:    "Time to produce a delta, by mode.",
			Buckets: prometheus.DefBuckets,
		}, []string{"mode"}),
		publishJobs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "plux_publish_jobs_total",
			Help: "Publish jobs by result.",
		}, []string{"result"}),
		publishDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "plux_publish_duration_seconds",
			Help:    "Time spent in each stage of the publish pipeline.",
			Buckets: prometheus.ExponentialBuckets(0.01, 3, 10),
		}, []string{"stage"}),
		jobsQueueDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "plux_jobs_queue_depth",
			Help: "Jobs waiting in each queue.",
		}, []string{"queue"}),
	}
	r.MustRegister(m.rpcRequests, m.rpcDuration, m.manifestRequests, m.deltaBytes,
		m.deltaGeneration, m.publishJobs, m.publishDuration, m.jobsQueueDepth)
	return m
}

// Handler serves the metrics endpoint.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{Registry: m.registry})
}

// Registry exposes the registry so that a component can register a
// collector of its own; the catalogued metrics stay behind the methods.
func (m *Metrics) Registry() *prometheus.Registry { return m.registry }

// RPC records one finished call. code is the Connect code, or "ok".
func (m *Metrics) RPC(service, method, code string, d time.Duration) {
	m.rpcRequests.WithLabelValues(service, method, code).Inc()
	m.rpcDuration.WithLabelValues(service, method).Observe(d.Seconds())
}

// Manifest records one manifest request; result is "not_modified",
// "updated" or "error".
func (m *Metrics) Manifest(environment, result string) {
	m.manifestRequests.WithLabelValues(environment, result).Inc()
}

// Download records the size a device is asked to fetch; kind is "delta"
// or "full".
func (m *Metrics) Download(kind string, bytes int64) {
	m.deltaBytes.WithLabelValues(kind).Observe(float64(bytes))
}

// DeltaGenerated records the time to produce a delta; mode is
// "precomputed" or "on_demand".
func (m *Metrics) DeltaGenerated(mode string, d time.Duration) {
	m.deltaGeneration.WithLabelValues(mode).Observe(d.Seconds())
}

// PublishFinished records a finished publish job; result is "succeeded",
// "failed" or "cancelled".
func (m *Metrics) PublishFinished(result string) { m.publishJobs.WithLabelValues(result).Inc() }

// PublishStage records the time one pipeline stage took.
func (m *Metrics) PublishStage(stage string, d time.Duration) {
	m.publishDuration.WithLabelValues(stage).Observe(d.Seconds())
}

// QueueDepth records how many jobs wait in a queue.
func (m *Metrics) QueueDepth(queue string, n int) {
	m.jobsQueueDepth.WithLabelValues(queue).Set(float64(n))
}
