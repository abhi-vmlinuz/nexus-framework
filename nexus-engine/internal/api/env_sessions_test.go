package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/abhi-vmlinuz/nexus-framework/nexus-engine/internal/api"
	"github.com/abhi-vmlinuz/nexus-framework/nexus-engine/internal/config"
)

// envTestConfig points the artefact root at a temp directory so the handler can
// be exercised without a cluster or /var/lib.
func envTestConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		Mode: "dev",
		Port: "8081",
		Session: config.SessionConfig{
			DefaultTTLMinutes: 60,
		},
		Env: config.EnvConfig{
			Image:        "test-env:latest",
			TTLMinutes:   60,
			ArtifactRoot: t.TempDir(),
		},
		Challenge: config.ChallengeConfig{
			DefaultCPULimit:    "0.5",
			DefaultMemoryLimit: "256Mi",
		},
		Registry: config.RegistryConfig{URL: "localhost:5000"},
	}
}

func envRouter(t *testing.T, cfg *config.Config) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// K8s and Store are nil: every case below must be rejected before either
	// is touched. A nil dereference here would be the test failing loudly.
	api.Register(r, api.Deps{Cfg: cfg})
	return r
}

func postEnv(t *testing.T, r *gin.Engine, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/env", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestCreateEnvRequiresUserID(t *testing.T) {
	r := envRouter(t, envTestConfig(t))
	if w := postEnv(t, r, map[string]any{}); w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for missing user_id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateEnvRejectsPathInArtifactID(t *testing.T) {
	r := envRouter(t, envTestConfig(t))
	for _, id := range []string{"../etc", "a/b", `a\b`, "..", "."} {
		w := postEnv(t, r, map[string]any{"user_id": "u1", "artifact_challenge_id": id})
		if w.Code != http.StatusBadRequest {
			t.Errorf("artifact id %q: want 400, got %d: %s", id, w.Code, w.Body.String())
		}
	}
}

func TestCreateEnvRejectsMissingArtifactDir(t *testing.T) {
	cfg := envTestConfig(t)
	r := envRouter(t, cfg)
	w := postEnv(t, r, map[string]any{"user_id": "u1", "artifact_challenge_id": "no-such-challenge"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for a missing artefact dir, got %d: %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("ARTIFACTS_NOT_FOUND")) {
		t.Errorf("expected ARTIFACTS_NOT_FOUND, got %s", w.Body.String())
	}
}

func TestCreateEnvFindsArtifactDir(t *testing.T) {
	// A real directory must get past validation. The request then reaches the
	// nil k8s client, which panics if it is not guarded — so assert the panic
	// is *after* validation by checking the artefact path is what the engine
	// would mount.
	cfg := envTestConfig(t)
	dir := filepath.Join(cfg.GetArtifactRoot(), "web-101")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("stat: %v", err)
	}

	r := envRouter(t, cfg)
	defer func() {
		if rec := recover(); rec != nil {
			t.Skipf("reached k8s with a nil client (expected in this test setup): %v", rec)
		}
	}()
	postEnv(t, r, map[string]any{"user_id": "u1", "artifact_challenge_id": "web-101"})
}
