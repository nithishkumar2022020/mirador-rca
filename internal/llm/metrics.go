package llm

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	requestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mirador_llm_requests_total",
			Help: "Total LLM requests attempted",
		},
		[]string{"outcome"},
	)
	requestLatency = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "mirador_llm_request_duration_seconds",
			Help:    "LLM request latency in seconds",
			Buckets: prometheus.ExponentialBuckets(0.005, 2, 10),
		},
	)
)

func init() {
	// Register metrics with the global registry. If already registered (e.g., tests), ignore error.
	_ = prometheus.Register(requestsTotal)
	_ = prometheus.Register(requestLatency)
}
