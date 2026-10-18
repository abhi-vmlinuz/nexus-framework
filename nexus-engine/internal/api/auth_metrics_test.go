package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/abhi-vmlinuz/nexus-framework/nexus-engine/internal/api"
	"github.com/abhi-vmlinuz/nexus-framework/nexus-engine/internal/config"
)

// newAuthTestRouter builds a router with no store or cluster dependency.
// Only route registration and middleware are exercised, so nil dependencies
// are safe: handlers dereference them at request time, not at register time.
func newAuthTestRouter(t *testing.T, apiKey string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{Mode: "prod", Port: "8081", APIKey: apiKey}
	r := gin.New()
	api.Register(r, api.Deps{Cfg: cfg})
	return r
}

func doGet(r *gin.Engine, path, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// /metrics exposes node, WireGuard, and per-session data. It must not be
// readable by anyone who can reach the engine port.
func TestMetricsRequiresAPIKey(t *testing.T) {
	r := newAuthTestRouter(t, "secret-key")

	if w := doGet(r, "/metrics", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated /metrics: got %d, want 401", w.Code)
	}
	if w := doGet(r, "/metrics", "wrong-key"); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong key /metrics: got %d, want 401", w.Code)
	}
	if w := doGet(r, "/metrics", "secret-key"); w.Code != http.StatusOK {
		t.Errorf("authenticated /metrics: got %d, want 200", w.Code)
	}
}

// /health stays public: load balancers and the installer probe it without
// credentials.
func TestHealthStaysPublic(t *testing.T) {
	r := newAuthTestRouter(t, "secret-key")

	if w := doGet(r, "/health", ""); w.Code != http.StatusOK {
		t.Errorf("unauthenticated /health: got %d, want 200", w.Code)
	}
}

// With no key configured the engine runs open, matching every other endpoint.
func TestMetricsOpenWhenNoKeyConfigured(t *testing.T) {
	r := newAuthTestRouter(t, "")

	if w := doGet(r, "/metrics", ""); w.Code != http.StatusOK {
		t.Errorf("no-key /metrics: got %d, want 200", w.Code)
	}
}

// API routes remain protected alongside the metrics change.
func TestAPIRoutesRequireAPIKey(t *testing.T) {
	r := newAuthTestRouter(t, "secret-key")

	if w := doGet(r, "/api/v1/challenges", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated /api/v1/challenges: got %d, want 401", w.Code)
	}
}
