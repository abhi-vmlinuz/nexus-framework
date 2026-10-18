package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type fileHandler struct {
	d Deps
}

func newFileHandler(d Deps) *fileHandler {
	return &fileHandler{d: d}
}

type WriteFileRequest struct {
	Filename string `json:"filename" binding:"required"`
	Content  string `json:"content"`
}

type ExecRequest struct {
	Command string `json:"command" binding:"required"`
	Timeout int    `json:"timeout_seconds,omitempty"` // default 15s
}

// WriteFile writes content to $HOME/<filename> in the student pod.
func (h *fileHandler) WriteFile(c *gin.Context) {
	sessionID := c.Param("id")

	// Verify session exists
	_, err := h.d.Store.GetSession(sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	var req WriteFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	if err := h.d.K8s.WriteHomeFile(ctx, sessionID, req.Filename, []byte(req.Content)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":   "success",
		"filename": req.Filename,
		"message":  "file written to $HOME successfully",
	})
}

// ReadFile reads $HOME/<filename> from the student pod.
func (h *fileHandler) ReadFile(c *gin.Context) {
	sessionID := c.Param("id")
	filename := c.Param("filename")

	// Verify session exists
	_, err := h.d.Store.GetSession(sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	data, err := h.d.K8s.ReadHomeFile(ctx, sessionID, filename)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"filename": filename,
		"content":  string(data),
	})
}

// Exec runs a command inside the student pod in $HOME and returns the output.
func (h *fileHandler) Exec(c *gin.Context) {
	sessionID := c.Param("id")

	// Verify session exists
	_, err := h.d.Store.GetSession(sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	var req ExecRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	timeoutSec := req.Timeout
	if timeoutSec <= 0 || timeoutSec > 60 {
		timeoutSec = 15
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Duration(timeoutSec)*time.Second)
	defer cancel()

	// Execute inside $HOME
	cmd := []string{"/bin/sh", "-c", "cd \"$HOME\" && " + req.Command}
	stdout, stderr, err := h.d.K8s.Exec(ctx, sessionID, "", cmd, nil)

	c.JSON(http.StatusOK, gin.H{
		"stdout":  stdout,
		"stderr":  stderr,
		"success": err == nil,
		"error":   errString(err),
	})
}

func errString(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}
