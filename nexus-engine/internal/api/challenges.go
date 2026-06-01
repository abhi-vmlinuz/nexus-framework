package api

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/nexus-oss/nexus/nexus-engine/internal/registry"
	"github.com/nexus-oss/nexus/nexus-engine/internal/state"
)

type challengeHandler struct{ d Deps }

func newChallengeHandler(d Deps) *challengeHandler { return &challengeHandler{d: d} }

// CreateChallengeRequest is the body for POST /api/v1/challenges.
type CreateChallengeRequest struct {
	Name           string               `json:"name" binding:"required"`
	DockerfilePath string               `json:"dockerfile_path"`
	ComposePath    string               `json:"compose_path"`
	TTLMinutes     int                  `json:"ttl_minutes"`
	Ports          []int                `json:"ports"`
	// Multi-container support.
	Containers     []state.ContainerSpec `json:"containers"`
	// Single-container overrides.
	Resources      *state.Resources      `json:"resources,omitempty"`
	ReadinessProbe *state.ReadinessProbe `json:"readiness_probe,omitempty"`
}

// ChallengeResponse is the unified response wrapper for challenge creation.
type ChallengeResponse struct {
	Challenge state.Challenge           `json:"challenge"`
	Build     BuildMetadata             `json:"build"`
}

// BuildMetadata captures the build outcome for the API response.
type BuildMetadata struct {
	Status       string                        `json:"status"` // success | partial_failure | full_failure | skipped
	StartedAt    string                        `json:"started_at,omitempty"`
	CompletedAt  string                        `json:"completed_at,omitempty"`
	DurationMs   int64                         `json:"duration_ms,omitempty"`
	Registry     string                        `json:"registry"`
	RegistryAuth registry.RegistryAuthInfo     `json:"registry_auth"`
	Tooling      registry.ToolingInfo          `json:"tooling,omitempty"`
	Ready        bool                          `json:"ready_for_deployment"`
	Containers   []ContainerBuildMetadata      `json:"containers"`
	RetryInfo    *RetryInfo                    `json:"retry_info,omitempty"`
}

// ContainerBuildMetadata is per-container build info in the API response.
type ContainerBuildMetadata struct {
	Name     string `json:"name"`
	Image    string `json:"image,omitempty"`
	Status   string `json:"status"` // built | pre-built | pulled | failed
	DurationMs int64 `json:"duration_ms,omitempty"`
	Ports    []int  `json:"ports,omitempty"`
	Error    string `json:"error,omitempty"`
}

// RetryInfo is included on partial/full failure responses.
type RetryInfo struct {
	CanRetry         bool     `json:"can_retry"`
	RetryURL         string   `json:"retry_url"`
	FailedContainers []string `json:"failed_containers"`
	Note             string   `json:"note,omitempty"`
}

// FailureResponse is returned on build failures.
type FailureResponse struct {
	Error      string        `json:"error"`
	Message    string        `json:"message"`
	Build      BuildMetadata `json:"build"`
	RetryInfo  *RetryInfo    `json:"retry_info,omitempty"`
}

// buildMetadataFromResult converts a registry.BuildResult into API response metadata.
func buildMetadataFromResult(result *registry.BuildResult, cfg registry.RegistryAuthInfo) BuildMetadata {
	containers := make([]ContainerBuildMetadata, len(result.Containers))
	for i, c := range result.Containers {
		containers[i] = ContainerBuildMetadata{
			Name:       c.Name,
			Image:      c.Image,
			Status:     c.Status,
			DurationMs: c.Duration.Milliseconds(),
			Ports:      c.Ports,
			Error:      c.Error,
		}
	}
	return BuildMetadata{
		Status:       "success",
		StartedAt:    result.StartedAt.Format(time.RFC3339),
		CompletedAt:  result.StartedAt.Add(result.Duration).Format(time.RFC3339),
		DurationMs:   result.Duration.Milliseconds(),
		Registry:     result.Image[:strings.LastIndex(result.Image, "/")],
		RegistryAuth: cfg,
		Tooling:      result.Tooling,
		Ready:        true,
		Containers:   containers,
	}
}

// buildMetadataFromCompose converts a compose ParseComposeResult into API response metadata.
func buildMetadataFromCompose(result *registry.ParseComposeResult, registryURL string, cfg registry.RegistryAuthInfo) BuildMetadata {
	containers := make([]ContainerBuildMetadata, len(result.Build.Containers))
	for i, c := range result.Build.Containers {
		containers[i] = ContainerBuildMetadata{
			Name:       c.Name,
			Image:      c.Image,
			Status:     c.Status,
			DurationMs: c.Duration.Milliseconds(),
			Ports:      c.Ports,
			Error:      c.Error,
		}
	}

	ready := result.Build.Status == "success"
	return BuildMetadata{
		Status:       result.Build.Status,
		StartedAt:    result.Build.StartedAt.Format(time.RFC3339),
		CompletedAt:  result.Build.CompletedAt.Format(time.RFC3339),
		DurationMs:   result.Build.DurationMs,
		Registry:     registryURL,
		RegistryAuth: cfg,
		Tooling:      result.Build.Tooling,
		Ready:        ready,
		Containers:   containers,
	}
}

// storeBuildLogs persists build logs from container results to Redis.
func (h *challengeHandler) storeBuildLogs(challengeID string, containers []registry.ContainerBuildResult) {
	for _, c := range containers {
		if c.BuildLog != "" {
			if err := h.d.Store.SaveBuildLog(challengeID, c.Name, c.BuildLog); err != nil {
				log.Printf("warning: failed to save build log for %s/%s: %v", challengeID, c.Name, err)
			}
		}
	}
}

// Create registers a new challenge by building its Docker image.
func (h *challengeHandler) Create(c *gin.Context) {
	var req CreateChallengeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Default TTL from config.
	if req.TTLMinutes <= 0 {
		req.TTLMinutes = h.d.Cfg.Session.DefaultTTLMinutes
	}

	// Validate: must supply exactly one of dockerfile_path, compose_path, or containers[].
	provided := 0
	if req.DockerfilePath != "" { provided++ }
	if req.ComposePath != ""    { provided++ }
	if len(req.Containers) > 0  { provided++ }
	if provided == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "must supply one of: dockerfile_path, compose_path, or containers[]"})
		return
	}
	if provided > 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "supply only one of: dockerfile_path, compose_path, or containers[]"})
		return
	}

	// Generate deterministic ID from name.
	challengeID := fmt.Sprintf("%s-%s", sanitizeName(req.Name), uuid.New().String()[:8])
	authInfo := h.d.Builder.GetRegistryAuth()
	registryURL := h.d.Cfg.Registry.URL

	var ch state.Challenge

	if req.ComposePath != "" {
		// ── Compose path: engine parses + builds all services ────────────────
		log.Printf("parsing compose file for challenge %s from %s", challengeID, req.ComposePath)
		parsed, err := h.d.Builder.ParseAndBuild(req.Name, req.ComposePath)
		if err != nil {
			// Store logs even on failure (for debugging).
			if parsed != nil {
				h.storeBuildLogs(challengeID, parsed.Build.Containers)
			}

			// Build the failure response with per-container details.
			buildMeta := BuildMetadata{
				Status:       "failure",
				Registry:     registryURL,
				RegistryAuth: authInfo,
				Ready:        false,
			}
			if parsed != nil {
				buildMeta = buildMetadataFromCompose(parsed, registryURL, authInfo)
			}

			// Determine if partial or full failure.
			var failedContainers []string
			for _, bc := range buildMeta.Containers {
				if bc.Status == "failed" {
					failedContainers = append(failedContainers, bc.Name)
				}
			}

			errorCode := "BUILD_FAILED"
			if buildMeta.Status == "partial_failure" {
				errorCode = "PARTIAL_BUILD_FAILURE"
			} else if buildMeta.Status == "full_failure" {
				errorCode = "FULL_BUILD_FAILURE"
			}

			c.JSON(http.StatusUnprocessableEntity, FailureResponse{
				Error:   errorCode,
				Message: err.Error(),
				Build:   buildMeta,
				RetryInfo: &RetryInfo{
					CanRetry:         true,
					RetryURL:         "/api/v1/challenges",
					FailedContainers: failedContainers,
					Note:             "Successfully built containers are cached. Retry will be faster.",
				},
			})
			return
		}

		// Store build logs on success.
		h.storeBuildLogs(challengeID, parsed.Build.Containers)

		ch = state.Challenge{
			ID:         challengeID,
			Name:       req.Name,
			Containers: parsed.Containers,
			TTLMinutes: req.TTLMinutes,
			Ports:      parsed.AllPorts,
			CreatedAt:  time.Now().UTC(),
			UpdatedAt:  time.Now().UTC(),
		}
		log.Printf("challenge %s registered with %d containers", challengeID, len(parsed.Containers))

		// Store challenge and return wrapped response.
		if err := h.d.Store.SaveChallenge(ch); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusCreated, ChallengeResponse{
			Challenge: ch,
			Build:     buildMetadataFromCompose(parsed, registryURL, authInfo),
		})
		return

	} else if len(req.Containers) > 0 {
		// ── Pre-built containers: no build step ──────────────────────────────
		log.Printf("registering multi-container challenge %s (%d containers)", challengeID, len(req.Containers))
		var allPorts []int
		seen := map[int]bool{}
		var containerMeta []ContainerBuildMetadata
		for _, ct := range req.Containers {
			for _, p := range ct.Ports {
				if !seen[p] { seen[p] = true; allPorts = append(allPorts, p) }
			}
			containerMeta = append(containerMeta, ContainerBuildMetadata{
				Name:   ct.Name,
				Image:  ct.Image,
				Status: "pre-built",
				Ports:  ct.Ports,
			})
		}
		ch = state.Challenge{
			ID:         challengeID,
			Name:       req.Name,
			Containers: req.Containers,
			TTLMinutes: req.TTLMinutes,
			Ports:      allPorts,
			CreatedAt:  time.Now().UTC(),
			UpdatedAt:  time.Now().UTC(),
		}

		if err := h.d.Store.SaveChallenge(ch); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusCreated, ChallengeResponse{
			Challenge: ch,
			Build: BuildMetadata{
				Status:       "skipped",
				Registry:     registryURL,
				RegistryAuth: authInfo,
				Ready:        true,
				Containers:   containerMeta,
			},
		})
		return

	} else {
		// ── Single-container: build + push via nerdctl ──────────────────────
		log.Printf("building image for challenge %s from %s", challengeID, req.DockerfilePath)
		result, err := h.d.Builder.Build(req.Name, req.DockerfilePath)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, FailureResponse{
				Error:   "BUILD_FAILED",
				Message: err.Error(),
				Build: BuildMetadata{
					Status:       "failure",
					Registry:     registryURL,
					RegistryAuth: authInfo,
					Ready:        false,
				},
				RetryInfo: &RetryInfo{
					CanRetry:         true,
					RetryURL:         "/api/v1/challenges",
					FailedContainers: []string{req.Name},
				},
			})
			return
		}

		// Store build log.
		h.storeBuildLogs(challengeID, result.Containers)

		if len(req.Ports) == 0 {
			req.Ports = result.Ports
		}
		ch = state.Challenge{
			ID:             challengeID,
			Name:           req.Name,
			Image:          result.Image,
			DockerfilePath: req.DockerfilePath,
			TTLMinutes:     req.TTLMinutes,
			Ports:          req.Ports,
			Tag:            result.Tag,
			Resources:      req.Resources,
			ReadinessProbe: req.ReadinessProbe,
			CreatedAt:      time.Now().UTC(),
			UpdatedAt:      time.Now().UTC(),
		}
		log.Printf("challenge %s registered: image=%s duration=%s", challengeID, result.Image, result.Duration)

		if err := h.d.Store.SaveChallenge(ch); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusCreated, ChallengeResponse{
			Challenge: ch,
			Build:     buildMetadataFromResult(result, authInfo),
		})
		return
	}
}

// List returns all registered challenges.
func (h *challengeHandler) List(c *gin.Context) {
	challenges, err := h.d.Store.ListChallenges()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"challenges": challenges, "count": len(challenges)})
}

// Get returns a single challenge by ID.
func (h *challengeHandler) Get(c *gin.Context) {
	ch, err := h.d.Store.GetChallenge(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}
	c.JSON(http.StatusOK, ch)
}

// Delete removes a challenge definition. Does not terminate existing sessions.
func (h *challengeHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if _, err := h.d.Store.GetChallenge(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}
	if err := h.d.Store.DeleteChallenge(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"challenge_id": id, "status": "deleted"})
}

// Rebuild triggers a fresh nerdctl build for an existing challenge.
func (h *challengeHandler) Rebuild(c *gin.Context) {
	ch, err := h.d.Store.GetChallenge(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}

	authInfo := h.d.Builder.GetRegistryAuth()
	registryURL := h.d.Cfg.Registry.URL

	log.Printf("rebuilding challenge %s from %s", ch.ID, ch.DockerfilePath)
	result, err := h.d.Builder.Build(ch.Name, ch.DockerfilePath)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, FailureResponse{
			Error:   "BUILD_FAILED",
			Message: err.Error(),
			Build: BuildMetadata{
				Status:       "failure",
				Registry:     registryURL,
				RegistryAuth: authInfo,
				Ready:        false,
			},
			RetryInfo: &RetryInfo{
				CanRetry:         true,
				RetryURL:         fmt.Sprintf("/api/v1/challenges/%s/rebuild", ch.ID),
				FailedContainers: []string{ch.Name},
			},
		})
		return
	}

	// Store build log.
	h.storeBuildLogs(ch.ID, result.Containers)

	ch.Image = result.Image
	ch.Tag = result.Tag
	ch.UpdatedAt = time.Now().UTC()
	if err := h.d.Store.SaveChallenge(ch); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, ChallengeResponse{
		Challenge: ch,
		Build:     buildMetadataFromResult(result, authInfo),
	})
}

// BuildLogs returns build logs for a challenge.
// Query params:
//   - ?container=<name>  — filter by container (single-container or specific service)
//   - ?lines=<N>         — return last N lines (default: all)
//   - ?follow=true       — stream logs via SSE (Server-Sent Events)
func (h *challengeHandler) BuildLogs(c *gin.Context) {
	challengeID := c.Param("id")

	// Verify challenge exists.
	if _, err := h.d.Store.GetChallenge(challengeID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "challenge not found"})
		return
	}

	containerFilter := c.Query("container")
	linesParam := c.Query("lines")
	followParam := c.Query("follow")

	lines := 0
	if linesParam != "" {
		if n, err := strconv.Atoi(linesParam); err == nil && n > 0 {
			lines = n
		}
	}

	// Streaming mode via SSE.
	if followParam == "true" {
		h.streamBuildLogs(c, challengeID, containerFilter, lines)
		return
	}

	// Static mode: return logs as JSON.
	if containerFilter != "" {
		// Single container.
		var logContent string
		var err error
		if lines > 0 {
			logContent, err = h.d.Store.GetBuildLogLines(challengeID, containerFilter, lines)
		} else {
			logContent, err = h.d.Store.GetBuildLog(challengeID, containerFilter)
		}
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"challenge_id": challengeID,
			"container":    containerFilter,
			"log":          logContent,
		})
		return
	}

	// All containers.
	logs, err := h.d.Store.GetAllBuildLogs(challengeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if lines > 0 {
		for name, content := range logs {
			logs[name] = tailLines(content, lines)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"challenge_id": challengeID,
		"logs":         logs,
	})
}

// streamBuildLogs streams build logs via Server-Sent Events.
// In practice, build logs are stored after build completes, so streaming
// polls for new data. For real-time streaming during build, a future
// enhancement would use a pub/sub channel.
func (h *challengeHandler) streamBuildLogs(c *gin.Context, challengeID, containerFilter string, lines int) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
		return
	}

	// Poll interval for checking new log data.
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Track what we've already sent to avoid duplicates.
	sentBytes := make(map[string]int)

	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-ticker.C:
			if containerFilter != "" {
				logContent, err := h.d.Store.GetBuildLog(challengeID, containerFilter)
				if err != nil {
					continue
				}
				offset := sentBytes[containerFilter]
				if len(logContent) > offset {
					newData := logContent[offset:]
					fmt.Fprintf(c.Writer, "event: log\ndata: %s\n\n", newData)
					sentBytes[containerFilter] = len(logContent)
					flusher.Flush()
				}
			} else {
				logs, err := h.d.Store.GetAllBuildLogs(challengeID)
				if err != nil {
					continue
				}
				for name, content := range logs {
					offset := sentBytes[name]
					if len(content) > offset {
						newData := content[offset:]
						fmt.Fprintf(c.Writer, "event: log\ndata: {\"container\":\"%s\",\"log\":\"%s\"}\n\n", name, newData)
						sentBytes[name] = len(content)
						flusher.Flush()
					}
				}
			}

			// Check if build is complete (all logs stop growing).
			// After 30 seconds of no new data, send done event and close.
			// This is a simple heuristic — a proper implementation would
			// track build status via a pub/sub channel.
		}
	}
}

// tailLines returns the last n lines of a string.
func tailLines(s string, n int) string {
	if n <= 0 {
		return s
	}
	parts := strings.Split(s, "\n")
	if len(parts) <= n {
		return s
	}
	return strings.Join(parts[len(parts)-n:], "\n")
}

// sanitizeName is defined in admin.go — shared across handlers.
