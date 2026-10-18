package api

import (
	"crypto/subtle"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/abhi-vmlinuz/nexus-framework/nexus-engine/internal/config"
)

// RequireAPIKey returns gin middleware that enforces API key authentication.
// Only /health is exempt, so liveness probes work without credentials.
// /metrics is NOT exempt: node, WireGuard, and per-session data must not be
// readable by anyone who can reach the engine port. Prometheus supplies the key
// with bearer_token_file.
// If no key is configured, all requests are allowed (auth disabled).
func RequireAPIKey(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Public endpoint — no auth required
		if c.Request.URL.Path == "/health" {
			c.Next()
			return
		}
		expected := cfg.APIKey
		if expected == "" {
			// No key configured — allow all (auth disabled)
			c.Next()
			return
		}
		provided := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			c.AbortWithStatusJSON(401, gin.H{"error": "unauthorized", "message": "missing or invalid API key"})
			return
		}
		c.Next()
	}
}
