// Package api registers all HTTP routes for nexus-engine.
package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nexus-oss/nexus/nexus-engine/internal/config"
	"github.com/nexus-oss/nexus/nexus-engine/internal/controller"
	"github.com/nexus-oss/nexus/nexus-engine/internal/k8s"
	"github.com/nexus-oss/nexus/nexus-engine/internal/nodeagent"
	"github.com/nexus-oss/nexus/nexus-engine/internal/registry"
	"github.com/nexus-oss/nexus/nexus-engine/internal/state"
	"golang.org/x/time/rate"
)

// Deps bundles all handler dependencies.
type Deps struct {
	Store      *state.Store
	K8s        *k8s.Client
	NodeAgent  *nodeagent.Client // may be nil in dev mode
	Builder    *registry.Builder
	Controller *controller.Controller
	Cfg        *config.Config
}

// Register wires all HTTP routes onto the gin engine.
func Register(r *gin.Engine, d Deps) {
	// Rate limiting middleware — 10 requests/sec per process (global).
	r.Use(RateLimit(10))

	// Health
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":    "healthy",
			"service":   "nexus-engine",
			"mode":      d.Cfg.Mode,
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	})

	// Metrics (prometheus)
	r.GET("/metrics", metricsHandler())

	// Auth middleware — protects all /api/v1/* endpoints
	r.Use(RequireAPIKey(d.Cfg))

	// Debug endpoints
	dbg := r.Group("/debug")
	{
		dbg.GET("/system", newDebugHandler(d).System)
		dbg.GET("/controller", newDebugHandler(d).Controller)
	}

	v1 := r.Group("/api/v1")
	{
		// Challenge management
		ch := newChallengeHandler(d)
		v1.POST("/challenges", ch.Create)
		v1.GET("/challenges", ch.List)
		v1.GET("/challenges/:id", ch.Get)
		v1.DELETE("/challenges/:id", ch.Delete)
		v1.POST("/challenges/:id/rebuild", ch.Rebuild)
		v1.GET("/challenges/:id/build-logs", ch.BuildLogs)

		// Session management
		sh := newSessionHandler(d)
		v1.POST("/sessions", sh.Create)
		v1.GET("/sessions", sh.List)
		v1.GET("/sessions/:id", sh.Get)
		v1.DELETE("/sessions/:id", sh.Terminate)
		v1.POST("/sessions/:id/extend", sh.Extend)

		// Admin / operator endpoints
		admin := v1.Group("/admin")
		h := newAdminHandler(d)
		admin.GET("/sessions", h.Sessions)
		admin.GET("/nodes", h.Nodes)
		admin.GET("/cluster/health", h.ClusterHealth)
		admin.GET("/config", h.Config)
		admin.PUT("/config", h.UpdateConfig)
		admin.PUT("/registry", h.UpdateRegistry)
		admin.POST("/reconcile", h.TriggerReconcile)

		// Cluster visibility
		admin.GET("/cluster/pods", h.GetClusterPods)
		admin.GET("/cluster/nodes", h.GetClusterNodes)
		admin.GET("/cluster/network-policies", h.GetNetworkPolicies)

		// Registry visibility
		admin.GET("/registry/images", h.GetRegistryImages)
		admin.GET("/registry/stats", h.GetRegistryStats)
		admin.GET("/registry/pulls", h.GetRegistryPulls)

		// VPN config (WireGuard peer provisioning)
		vh := newVPNHandler(d)
		v1.GET("/vpn/config", vh.Config)
		v1.GET("/vpn/status", vh.Status)
		v1.POST("/vpn/regenerate", vh.Regenerate)
	}
}

// RateLimit returns a gin middleware that limits requests to rps per second
// using a token-bucket rate limiter. Excess requests receive HTTP 429.
func RateLimit(rps float64) gin.HandlerFunc {
	limiter := rate.NewLimiter(rate.Limit(rps), 10)
	return func(c *gin.Context) {
		if !limiter.Allow() {
			c.AbortWithStatusJSON(429, gin.H{"error": "rate limit exceeded"})
			return
		}
		c.Next()
	}
}
