package api

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	metricSessionCreateDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "nexus_session_create_duration_seconds",
		Help:    "Session create latency including pod spawn wait.",
		Buckets: []float64{1, 2, 5, 10, 30, 60, 90},
	}, []string{"status"})

	metricSessionCreateTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "nexus_session_create_total",
		Help: "Session create attempts by status.",
	}, []string{"status"})

	metricVPNClaimDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "nexus_vpn_claim_duration_seconds",
		Help:    "VPN IP claim + peer provision latency.",
		Buckets: []float64{0.1, 0.5, 1, 2, 5, 10},
	}, []string{"status"})

	metricVPNClaimTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "nexus_vpn_claim_total",
		Help: "VPN claims by status (claimed, cached, exhausted).",
	}, []string{"status"})

	metricActiveSessions = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "nexus_active_sessions",
		Help: "Current active sessions (running + creating).",
	})

	metricVPNIPsUsed = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "nexus_vpn_ips_used",
		Help: "Current VPN IPs allocated in 10.8.0.0/22 pool.",
	})
)
