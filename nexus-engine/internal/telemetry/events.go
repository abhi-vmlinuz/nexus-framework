// Package telemetry provides file-backed event logging for competition pitch data.
// Events are appended as JSONL to /var/lib/nexus/events.jsonl (0600, fsync per write)
// so they survive Redis restarts. Prometheus metrics remain in-memory for live dashboards.
package telemetry

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// EventType enumerates pitch-relevant lifecycle events.
type EventType string

const (
	SessionCreated EventType = "session_created"
	SessionReady   EventType = "session_ready"
	SessionFailed  EventType = "session_failed"
	SessionExpired EventType = "session_expired"
	VPNClaimed     EventType = "vpn_claimed"
	VPNExhausted   EventType = "vpn_exhausted"
	ReconcileRepair EventType = "reconcile_repair"
)

// Event is one JSONL row. Keep fields stable — Gemini builds Grafana/report on this schema.
type Event struct {
	TS          string `json:"ts"`
	Type        string `json:"type"`
	UserID      string `json:"user_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	ChallengeID string `json:"challenge_id,omitempty"`
	DurationMs  int64  `json:"duration_ms,omitempty"`
	Status      string `json:"status,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

// Logger appends events to a JSONL file. Safe for concurrent use.
type Logger struct {
	mu   sync.Mutex
	path string
}

// DefaultPath is the on-disk event log location (volume-backed, not Redis).
const DefaultPath = "/var/lib/nexus/events.jsonl"

// New creates a Logger, ensuring parent dir exists with 0700.
func New(path string) (*Logger, error) {
	if path == "" {
		path = DefaultPath
	}
	if err := os.MkdirAll(dirOf(path), 0o700); err != nil {
		return nil, err
	}
	return &Logger{path: path}, nil
}

// Append writes one event with fsync. Never returns error to callers that must not fail.
func (l *Logger) Append(e Event) {
	if l == nil {
		return
	}
	if e.TS == "" {
		e.TS = time.Now().UTC().Format(time.RFC3339Nano)
	}
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
	_ = f.Sync()
}

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			if i == 0 {
				return "/"
			}
			return p[:i]
		}
	}
	return "."
}
