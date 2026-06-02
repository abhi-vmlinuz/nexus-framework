package api

import (
	"crypto/subtle"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nexus-oss/nexus/nexus-engine/internal/config"
)

// RequireAPIKey returns gin middleware that enforces API key authentication.
// Public endpoints (/health, /metrics) are exempt.
// If no key is configured, all requests are allowed (auth disabled).
func RequireAPIKey(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Public endpoints — no auth required
		if c.Request.URL.Path == "/health" || c.Request.URL.Path == "/metrics" {
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
