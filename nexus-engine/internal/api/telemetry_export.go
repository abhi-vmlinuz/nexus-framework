package api

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// TelemetryExport streams /var/lib/nexus/events.jsonl filtered by query params.
// GET /api/v1/admin/telemetry/export?format=json|csv&from=RFC3339&to=RFC3339&type=session_created
// CSV header: ts,type,user_id,session_id,challenge_id,duration_ms,status
func (h *adminHandler) TelemetryExport(c *gin.Context) {
	format := strings.ToLower(c.DefaultQuery("format", "json"))
	typ := c.Query("type")
	var from, to time.Time
	if s := c.Query("from"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			from = t
		}
	}
	if s := c.Query("to"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			to = t
		}
	}

	path := "/var/lib/nexus/events.jsonl"
	if p := c.Query("path"); p != "" {
		// Allow override only under /var/lib/nexus or /tmp for tests.
		if strings.HasPrefix(p, "/var/lib/nexus/") || strings.HasPrefix(p, "/tmp/") {
			path = p
		}
	}

	f, err := os.Open(path)
	if err != nil {
		// No events yet — return empty set, not 500.
		if format == "csv" {
			c.Header("Content-Type", "text/csv")
			c.Header("Content-Disposition", "attachment; filename=nexus-telemetry.csv")
			c.String(http.StatusOK, "ts,type,user_id,session_id,challenge_id,duration_ms,status\n")
			return
		}
		c.JSON(http.StatusOK, gin.H{"events": []any{}, "count": 0})
		return
	}
	defer f.Close()

	type row struct {
		TS          string `json:"ts"`
		Type        string `json:"type"`
		UserID      string `json:"user_id"`
		SessionID   string `json:"session_id"`
		ChallengeID string `json:"challenge_id"`
		DurationMs  int64  `json:"duration_ms"`
		Status      string `json:"status"`
	}

	var rows []row
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		var r row
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue
		}
		if typ != "" && r.Type != typ {
			continue
		}
		if !from.IsZero() || !to.IsZero() {
			if t, err := time.Parse(time.RFC3339Nano, r.TS); err == nil {
				if !from.IsZero() && t.Before(from) {
					continue
				}
				if !to.IsZero() && t.After(to) {
					continue
				}
			} else if t, err := time.Parse(time.RFC3339, r.TS); err == nil {
				if !from.IsZero() && t.Before(from) {
					continue
				}
				if !to.IsZero() && t.After(to) {
					continue
				}
			}
		}
		rows = append(rows, r)
	}

	if format == "csv" {
		c.Header("Content-Type", "text/csv")
		c.Header("Content-Disposition", "attachment; filename=nexus-telemetry.csv")
		w := csv.NewWriter(c.Writer)
		_ = w.Write([]string{"ts", "type", "user_id", "session_id", "challenge_id", "duration_ms", "status"})
		for _, r := range rows {
			_ = w.Write([]string{r.TS, r.Type, r.UserID, r.SessionID, r.ChallengeID, itoa(r.DurationMs), r.Status})
		}
		w.Flush()
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": rows, "count": len(rows)})
}

func itoa(n int64) string {
	return strings.TrimSpace(strings.Replace(strings.Replace(jsonNumber(n), "\"", "", -1), "\n", "", -1))
}

func jsonNumber(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
