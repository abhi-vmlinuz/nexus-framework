package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/abhi-vmlinuz/nexus-framework/nexus-engine/internal/k8s"
	"github.com/abhi-vmlinuz/nexus-framework/nexus-engine/internal/state"
	"github.com/abhi-vmlinuz/nexus-framework/nexus-engine/internal/telemetry"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type sessionHandler struct{ d Deps }

func newSessionHandler(d Deps) *sessionHandler { return &sessionHandler{d: d} }

// CreateSessionRequest is the body for POST /api/v1/sessions.
type CreateSessionRequest struct {
	ChallengeID string `json:"challenge_id" binding:"required"`
	// UserID is required — used for VPN-based network isolation.
	UserID string `json:"user_id" binding:"required"`
	// VpnIP is the WireGuard-assigned IP for this user.
	// Required in prod mode for EnsureUserIsolation; ignored in dev mode.
	VpnIP string `json:"vpn_ip,omitempty"`
	// Role distinguishes an environment a participant works in from a target
	// they attack. Empty means "challenge".
	Role string `json:"role,omitempty"`
}

// Create spawns a new challenge pod and returns the pod IP.
//
// Flow:
//  1. Validate challenge exists
//  2. Enforce per-user session limit (if configured)
//  3. Create pod in k3s
//  4. Call node agent: GrantPodAccess + EnsureUserIsolation (prod mode)
//  5. Save session to Redis
//  6. Touch desired version → enqueue reconcile
func (h *sessionHandler) Create(c *gin.Context) {
	var req CreateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate challenge.
	ch, err := h.d.Store.GetChallenge(req.ChallengeID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "CHALLENGE_NOT_FOUND",
			"message": fmt.Sprintf("challenge %q does not exist", req.ChallengeID),
		})
		return
	}

	// Validate role before anything is created, so a typo is a 400 and not a
	// pod that the quota and policy selectors cannot classify.
	role, err := k8s.NormalizeRole(req.Role)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "INVALID_ROLE",
			"message": err.Error(),
		})
		return
	}

	// Enforce session limit if configured.
	maxPerUser := h.d.Cfg.GetMaxPerUser()
	if maxPerUser > 0 {
		count, _ := h.d.Store.CountUserActiveSessions(req.UserID)
		if count >= maxPerUser {
			c.JSON(http.StatusConflict, gin.H{
				"error":   "SESSION_LIMIT_REACHED",
				"message": fmt.Sprintf("user %q already has %d session(s), limit is %d", req.UserID, count, maxPerUser),
			})
			return
		}
	}

	// Prod mode: vpn_ip is required for isolation.
	if h.d.Cfg.IsProd() && req.VpnIP == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "VPN_IP_REQUIRED",
			"message": "vpn_ip is required in prod mode for network isolation",
		})
		return
	}

	sessionID := fmt.Sprintf("sess-%s", uuid.New().String()[:8])
	log.Printf("session %s: creating pod for user=%s challenge=%s", sessionID, req.UserID, req.ChallengeID)
	createStart := time.Now()

	// Build the spawn request, branching on challenge type.
	spawnReq := k8s.SpawnRequest{
		SessionID:   sessionID,
		ChallengeID: req.ChallengeID,
		UserID:      req.UserID,
		TTLMinutes:  ch.TTLMinutes,
		Role:        role,
	}
	if h.d.Cfg.Registry.AuthType != "none" {
		spawnReq.ImagePullSecrets = []string{"nexus-pull-secret"}
	}
	if ch.IsMultiContainer() {
		for _, ct := range ch.Containers {
			spawnReq.Containers = append(spawnReq.Containers, k8s.ContainerSpec{
				Name:           ct.Name,
				Image:          ct.Image,
				Ports:          ct.Ports,
				Env:            ct.Env,
				Resources:      h.mapToK8sResources(ct.Resources),
				ReadinessProbe: h.mapToK8sProbe(ct.ReadinessProbe),
			})
		}
	} else {
		spawnReq.Image = ch.Image
		spawnReq.Ports = ch.Ports
		spawnReq.Resources = h.mapToK8sResources(ch.Resources)
		spawnReq.ReadinessProbe = h.mapToK8sProbe(ch.ReadinessProbe)
	}

	// Spawn pod.
	podInfo, err := h.d.K8s.SpawnPod(spawnReq)
	if err != nil {
		log.Printf("session %s: pod spawn failed: %v", sessionID, err)
		metricSessionCreateTotal.WithLabelValues("failed").Inc()
		metricSessionCreateDuration.WithLabelValues("failed").Observe(time.Since(createStart).Seconds())
		if h.d.Telemetry != nil {
			h.d.Telemetry.Append(telemetry.Event{Type: string(telemetry.SessionFailed), UserID: req.UserID, SessionID: sessionID, ChallengeID: req.ChallengeID, DurationMs: time.Since(createStart).Milliseconds(), Status: "failed", Detail: err.Error()})
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "POD_SPAWN_FAILED",
			"message": err.Error(),
		})
		return
	}

	// Build session record.
	now := time.Now().UTC()
	sess := state.Session{
		ID:          sessionID,
		UserID:      req.UserID,
		ChallengeID: req.ChallengeID,
		PodName:     podInfo.PodName,
		PodIP:       podInfo.PodIP,
		Status:      "running",
		CreatedAt:   now,
		ExpiresAt:   now.Add(time.Duration(ch.TTLMinutes) * time.Minute),
		VpnIP:       req.VpnIP,
	}

	// Grant network access via node agent (prod mode only).
	if h.d.NodeAgent != nil && podInfo.PodIP != "" {
		ctx := context.Background()

		if err := h.d.NodeAgent.GrantPodAccess(ctx, req.UserID, podInfo.PodIP); err != nil {
			log.Printf("session %s: GrantPodAccess failed (non-fatal): %v", sessionID, err)
		}

		if req.VpnIP != "" {
			if err := h.d.NodeAgent.EnsureUserIsolation(ctx, req.UserID, req.VpnIP); err != nil {
				log.Printf("session %s: EnsureUserIsolation failed (non-fatal): %v", sessionID, err)
			}
		}

		// Save grant record.
		if err := h.d.Store.SaveGrant(state.GrantRecord{
			SessionID: sessionID,
			UserID:    req.UserID,
			PodIP:     podInfo.PodIP,
			Status:    "applied",
			GrantedAt: now,
		}); err != nil {
			log.Printf("WARNING: failed to save grant for session %s: %v", sessionID, err)
		}
	}

	// Persist session.
	if err := h.d.Store.SaveSession(sess); err != nil {
		// Best-effort cleanup: delete the pod we just created.
		h.d.K8s.TerminatePod(sessionID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save session"})
		return
	}

	// Enqueue reconciliation.
	if _, err := h.d.Store.TouchDesiredVersion(sessionID, "session_create"); err != nil {
		log.Printf("session %s: touch desired version failed: %v", sessionID, err)
	}
	h.d.Controller.Touch(sessionID, "session_create")

	log.Printf("session %s: created | pod_ip=%s", sessionID, podInfo.PodIP)
	metricSessionCreateTotal.WithLabelValues("created").Inc()
	metricSessionCreateDuration.WithLabelValues("created").Observe(time.Since(createStart).Seconds())
	metricActiveSessions.Inc()
	if h.d.Telemetry != nil {
		h.d.Telemetry.Append(telemetry.Event{Type: string(telemetry.SessionCreated), UserID: req.UserID, SessionID: sessionID, ChallengeID: req.ChallengeID, DurationMs: time.Since(createStart).Milliseconds(), Status: "created"})
		h.d.Telemetry.Append(telemetry.Event{Type: string(telemetry.SessionReady), UserID: req.UserID, SessionID: sessionID, ChallengeID: req.ChallengeID, DurationMs: time.Since(createStart).Milliseconds(), Status: "ready"})
	}

	type ServiceInfo struct {
		Name string `json:"name"`
		Port int    `json:"port"`
	}
	var services []ServiceInfo
	if ch.IsMultiContainer() {
		for _, ct := range ch.Containers {
			for _, p := range ct.Ports {
				services = append(services, ServiceInfo{Name: ct.Name, Port: p})
			}
		}
	} else {
		for _, p := range ch.Ports {
			services = append(services, ServiceInfo{Name: "main", Port: p})
		}
	}

	c.JSON(http.StatusCreated, gin.H{
		"session_id":   sess.ID,
		"user_id":      sess.UserID,
		"challenge_id": sess.ChallengeID,
		"pod_ip":       sess.PodIP,
		"pod_name":     sess.PodName,
		"status":       sess.Status,
		"created_at":   sess.CreatedAt,
		"expires_at":   sess.ExpiresAt,
		"ports":        ch.AllPorts(),
		"services":     services,
	})
}

// CreateEnvSessionRequest is the body for POST /api/v1/sessions/env.
//
// An environment session is the browser AttackBox: a pod a participant works
// in, not a target they attack. It has no challenge record, so it skips the
// challenge lookup that POST /api/v1/sessions requires. Creating a fake
// challenge row instead would leak a non-challenge into the challenge list and
// the leaderboard.
type CreateEnvSessionRequest struct {
	UserID string `json:"user_id" binding:"required"`

	// Image overrides the configured environment image. Empty means
	// NEXUS_ENV_IMAGE.
	Image string `json:"image,omitempty"`

	// TTLMinutes overrides the configured lifetime, capped at envMaxTTLMinutes.
	TTLMinutes int `json:"ttl_minutes,omitempty"`

	// ArtifactChallengeID names a directory under the engine's artefact root to
	// mount read-only at /artifacts. It is an identifier, not a path: the engine
	// joins it to NEXUS_ARTIFACT_ROOT, so a caller cannot ask for /etc.
	ArtifactChallengeID string `json:"artifact_challenge_id,omitempty"`

	VpnIP string `json:"vpn_ip,omitempty"`
}

const (
	// envMaxTTLMinutes bounds an AttackBox lifetime. An environment pod holds a
	// real shell, so it must not be able to outlive the event by accident.
	envMaxTTLMinutes = 240
	// envArtifactMountPath is where challenge files appear inside an env pod.
	envArtifactMountPath = "/artifacts"
)

// validArtifactID rejects anything that could escape the artefact root.
func validArtifactID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	return !strings.ContainsAny(id, `/\`) && !strings.Contains(id, "..")
}

// CreateEnv spawns an environment pod (browser AttackBox) for a user.
func (h *sessionHandler) CreateEnv(c *gin.Context) {
	var req CreateEnvSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ttl := h.d.Cfg.GetEnvTTLMinutes()
	if req.TTLMinutes > 0 {
		ttl = req.TTLMinutes
	}
	if ttl > envMaxTTLMinutes {
		ttl = envMaxTTLMinutes
	}

	image := req.Image
	if image == "" {
		image = h.d.Cfg.GetEnvImage()
	}

	var volumes []k8s.VolumeMount
	if req.ArtifactChallengeID != "" {
		if !validArtifactID(req.ArtifactChallengeID) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "INVALID_ARTIFACT_ID",
				"message": "artifact_challenge_id must be a plain identifier, not a path",
			})
			return
		}
		hostPath := filepath.Join(h.d.Cfg.GetArtifactRoot(), req.ArtifactChallengeID)
		info, err := os.Stat(hostPath)
		if err != nil || !info.IsDir() {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "ARTIFACTS_NOT_FOUND",
				"message": fmt.Sprintf("no artefact directory for %q at %s", req.ArtifactChallengeID, hostPath),
			})
			return
		}
		volumes = append(volumes, k8s.VolumeMount{
			Name:      "artifacts",
			HostPath:  hostPath,
			MountPath: envArtifactMountPath,
			// ReadWrite is left false: the participant gets the files, not the
			// right to modify the shared copy every other participant reads.
		})
	}

	maxPerUser := h.d.Cfg.GetMaxPerUser()
	if maxPerUser > 0 {
		count, _ := h.d.Store.CountUserActiveSessions(req.UserID)
		if count >= maxPerUser {
			c.JSON(http.StatusConflict, gin.H{
				"error":   "SESSION_LIMIT_REACHED",
				"message": fmt.Sprintf("user %q already has %d session(s), limit is %d", req.UserID, count, maxPerUser),
			})
			return
		}
	}

	sessionID := fmt.Sprintf("sess-%s", uuid.New().String()[:8])
	createStart := time.Now()
	log.Printf("session %s: creating env pod for user=%s image=%s", sessionID, req.UserID, image)

	spawnReq := k8s.SpawnRequest{
		SessionID:  sessionID,
		UserID:     req.UserID,
		TTLMinutes: ttl,
		Role:       k8s.RoleEnv,
		Image:      image,
		Resources: &k8s.Resources{
			CPU:    h.d.Cfg.GetDefaultCPULimit(),
			Memory: h.d.Cfg.GetDefaultMemoryLimit(),
		},
		Volumes: volumes,
	}
	if h.d.Cfg.Registry.AuthType != "none" {
		spawnReq.ImagePullSecrets = []string{"nexus-pull-secret"}
	}

	podInfo, err := h.d.K8s.SpawnPod(spawnReq)
	if err != nil {
		log.Printf("session %s: env pod spawn failed: %v", sessionID, err)
		metricSessionCreateTotal.WithLabelValues("failed").Inc()
		metricSessionCreateDuration.WithLabelValues("failed").Observe(time.Since(createStart).Seconds())
		if h.d.Telemetry != nil {
			h.d.Telemetry.Append(telemetry.Event{Type: string(telemetry.SessionFailed), UserID: req.UserID, SessionID: sessionID, DurationMs: time.Since(createStart).Milliseconds(), Status: "failed", Detail: err.Error()})
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "POD_SPAWN_FAILED",
			"message": err.Error(),
		})
		return
	}

	now := time.Now().UTC()
	sess := state.Session{
		ID:        sessionID,
		UserID:    req.UserID,
		Role:      k8s.RoleEnv,
		PodName:   podInfo.PodName,
		PodIP:     podInfo.PodIP,
		Status:    "running",
		CreatedAt: now,
		ExpiresAt: now.Add(time.Duration(ttl) * time.Minute),
		VpnIP:     req.VpnIP,
	}

	if h.d.NodeAgent != nil && podInfo.PodIP != "" {
		ctx := context.Background()
		if err := h.d.NodeAgent.GrantPodAccess(ctx, req.UserID, podInfo.PodIP); err != nil {
			log.Printf("session %s: GrantPodAccess failed (non-fatal): %v", sessionID, err)
		}
		if req.VpnIP != "" {
			if err := h.d.NodeAgent.EnsureUserIsolation(ctx, req.UserID, req.VpnIP); err != nil {
				log.Printf("session %s: EnsureUserIsolation failed (non-fatal): %v", sessionID, err)
			}
		}
		if err := h.d.Store.SaveGrant(state.GrantRecord{
			SessionID: sessionID,
			UserID:    req.UserID,
			PodIP:     podInfo.PodIP,
			Status:    "applied",
			GrantedAt: now,
		}); err != nil {
			log.Printf("WARNING: failed to save grant for session %s: %v", sessionID, err)
		}
	}

	if err := h.d.Store.SaveSession(sess); err != nil {
		log.Printf("session %s: save failed: %v", sessionID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save session"})
		return
	}

	if _, err := h.d.Store.TouchDesiredVersion(sessionID, "env_create"); err != nil {
		log.Printf("session %s: touch desired version failed: %v", sessionID, err)
	}
	h.d.Controller.Touch(sessionID, "env_create")

	metricSessionCreateTotal.WithLabelValues("created").Inc()
	metricSessionCreateDuration.WithLabelValues("created").Observe(time.Since(createStart).Seconds())
	metricActiveSessions.Inc()
	if h.d.Telemetry != nil {
		h.d.Telemetry.Append(telemetry.Event{Type: string(telemetry.SessionCreated), UserID: req.UserID, SessionID: sessionID, DurationMs: time.Since(createStart).Milliseconds(), Status: "created"})
		h.d.Telemetry.Append(telemetry.Event{Type: string(telemetry.SessionReady), UserID: req.UserID, SessionID: sessionID, DurationMs: time.Since(createStart).Milliseconds(), Status: "ready"})
	}

	log.Printf("session %s: env pod created | pod_ip=%s", sessionID, podInfo.PodIP)
	c.JSON(http.StatusCreated, gin.H{
		"session_id":        sess.ID,
		"user_id":           sess.UserID,
		"role":              k8s.RoleEnv,
		"pod_ip":            sess.PodIP,
		"pod_name":          sess.PodName,
		"status":            sess.Status,
		"created_at":        sess.CreatedAt,
		"expires_at":        sess.ExpiresAt,
		"ttl_minutes":       ttl,
		"artifacts_mounted": len(volumes) > 0,
	})
}

func (h *sessionHandler) mapToK8sResources(r *state.Resources) *k8s.Resources {
	cpu := h.d.Cfg.GetDefaultCPULimit()
	mem := h.d.Cfg.GetDefaultMemoryLimit()

	if r != nil {
		if r.CPU != "" {
			cpu = r.CPU
		}
		if r.Memory != "" {
			mem = r.Memory
		}
	}

	return &k8s.Resources{
		CPU:    cpu,
		Memory: mem,
	}
}

func (h *sessionHandler) mapToK8sProbe(p *state.ReadinessProbe) *k8s.ReadinessProbe {
	if p == nil {
		return nil
	}
	probe := &k8s.ReadinessProbe{
		InitialDelaySeconds: p.InitialDelaySeconds,
		PeriodSeconds:       p.PeriodSeconds,
		TimeoutSeconds:      p.TimeoutSeconds,
		FailureThreshold:    p.FailureThreshold,
	}
	if p.HTTPGet != nil {
		probe.HTTPGet = &k8s.HTTPGetAction{Path: p.HTTPGet.Path, Port: p.HTTPGet.Port}
	}
	if p.TCPSocket != nil {
		probe.TCPSocket = &k8s.TCPSocketAction{Port: p.TCPSocket.Port}
	}
	if p.Exec != nil {
		probe.Exec = &k8s.ExecAction{Command: p.Exec.Command}
	}
	return probe
}

// List returns all active sessions. In admin context all sessions; for user context filtered.
func (h *sessionHandler) List(c *gin.Context) {
	sessions, err := h.d.Store.ListAllSessions()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sessions": sessions, "count": len(sessions)})
}

// Get returns a single session by ID.
func (h *sessionHandler) Get(c *gin.Context) {
	sess, err := h.d.Store.GetSession(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	c.JSON(http.StatusOK, sess)
}

// Terminate deletes the pod and marks the session terminated.
func (h *sessionHandler) Terminate(c *gin.Context) {
	id := c.Param("id")
	sess, err := h.d.Store.GetSession(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	// Revoke network access.
	if h.d.NodeAgent != nil && sess.PodIP != "" {
		ctx := context.Background()
		if err := h.d.NodeAgent.RevokePodAccess(ctx, sess.UserID, sess.PodIP); err != nil {
			log.Printf("session %s: RevokePodAccess failed (non-fatal): %v", id, err)
		}
		if sess.VpnIP != "" {
			if err := h.d.NodeAgent.RevokeUserIsolation(ctx, sess.UserID, sess.VpnIP); err != nil {
				log.Printf("session %s: RevokeUserIsolation failed (non-fatal): %v", id, err)
			}
		}
		h.d.Store.DeleteGrant(sess.PodIP)
	}

	// Terminate pod.
	if err := h.d.K8s.TerminatePod(id); err != nil {
		log.Printf("session %s: TerminatePod failed (non-fatal): %v", id, err)
	}

	// Update session status.
	sess.Status = "terminated"
	h.d.Store.UpdateSession(sess)
	metricActiveSessions.Dec()
	if h.d.Telemetry != nil {
		h.d.Telemetry.Append(telemetry.Event{Type: string(telemetry.SessionExpired), UserID: sess.UserID, SessionID: id, ChallengeID: sess.ChallengeID, Status: "terminated"})
	}

	if _, err := h.d.Store.TouchDesiredVersion(id, "session_terminate"); err != nil {
		log.Printf("session %s: touch desired version failed: %v", id, err)
	}
	h.d.Controller.Touch(id, "session_terminate")

	c.JSON(http.StatusOK, gin.H{"session_id": id, "status": "terminated"})
}

// ExtendRequest is the body for POST /api/v1/sessions/:id/extend.
type ExtendRequest struct {
	DurationMinutes int `json:"duration_minutes"`
}

// Extend adds time to a session's TTL.
func (h *sessionHandler) Extend(c *gin.Context) {
	id := c.Param("id")
	sess, err := h.d.Store.GetSession(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	var req ExtendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.DurationMinutes <= 0 {
		req.DurationMinutes = h.d.Cfg.GetDefaultTTLMinutes()
	}

	oldExpiry := sess.ExpiresAt
	newExpiry := oldExpiry.Add(time.Duration(req.DurationMinutes) * time.Minute)

	// 1. Extend Redis TTL first — if this fails, we don't touch K8s.
	if err := h.d.Store.ExtendSession(id, req.DurationMinutes); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to extend session in store: %v", err)})
		return
	}

	// 2. Then extend K8s pod TTL (if this fails, Redis is ahead — safer than opposite).
	// Kubernetes expects this to be the total lifetime since pod creation.
	newTotalLifetime := int(newExpiry.Sub(sess.CreatedAt).Minutes())
	if err := h.d.K8s.ExtendPodTTL(id, newTotalLifetime); err != nil {
		log.Printf("WARNING: K8s extend failed but Redis extended: %v", err)
	}

	if _, err := h.d.Store.TouchDesiredVersion(id, "session_extend"); err != nil {
		log.Printf("session %s: touch desired version failed: %v", id, err)
	}

	c.JSON(http.StatusOK, gin.H{
		"session_id":     id,
		"status":         sess.Status,
		"old_expires_at": oldExpiry.Format(time.RFC3339),
		"new_expires_at": oldExpiry.Add(time.Duration(req.DurationMinutes) * time.Minute).Format(time.RFC3339),
	})
}
