package api

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"k8s.io/client-go/tools/remotecommand"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for dev / proxying
	},
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
}

type resizePayload struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

type sizeQueue struct {
	ch chan remotecommand.TerminalSize
}

func (q *sizeQueue) Next() *remotecommand.TerminalSize {
	size, ok := <-q.ch
	if !ok {
		return nil
	}
	return &size
}

type wsWriter struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (w *wsWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	err := w.conn.WriteMessage(websocket.TextMessage, p)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

type terminalHandler struct {
	d Deps
}

func newTerminalHandler(d Deps) *terminalHandler {
	return &terminalHandler{d: d}
}

// Connect upgrades HTTP to WebSocket and connects directly to the container's interactive shell.
func (h *terminalHandler) Connect(c *gin.Context) {
	sessionID := c.Param("id")

	// Validate session exists in store
	_, err := h.d.Store.GetSession(sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("terminal: websocket upgrade failed: %v", err)
		return
	}
	defer ws.Close()

	// Setup stdin pipe
	stdinReader, stdinWriter := io.Pipe()
	stdoutWriter := &wsWriter{conn: ws}

	// Setup resize queue
	szQueue := &sizeQueue{ch: make(chan remotecommand.TerminalSize, 10)}
	// Default starting size
	szQueue.ch <- remotecommand.TerminalSize{Width: 80, Height: 24}

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	// Goroutine to read from WebSocket (stdin + resize events)
	go func() {
		defer stdinWriter.Close()
		defer cancel()

		for {
			msgType, data, err := ws.ReadMessage()
			if err != nil {
				return
			}

			// Check if message is a JSON resize event
			if msgType == websocket.TextMessage && len(data) > 0 && data[0] == '{' {
				var res resizePayload
				if err := json.Unmarshal(data, &res); err == nil && res.Type == "resize" && res.Cols > 0 && res.Rows > 0 {
					select {
					case szQueue.ch <- remotecommand.TerminalSize{Width: res.Cols, Height: res.Rows}:
					default:
					}
					continue
				}
			}

			// Otherwise write directly to stdin pipe
			if _, err := stdinWriter.Write(data); err != nil {
				return
			}
		}
	}()

	// Shell command: open bash if present, else fallback to sh.
	// Starts in default container working directory ($HOME).
	cmd := []string{"/bin/sh", "-c", "if [ -x /bin/bash ]; then exec /bin/bash -l; else exec /bin/sh -l; fi"}

	// Stream execution to pod
	err = h.d.K8s.ExecStream(ctx, sessionID, "", cmd, true, stdinReader, stdoutWriter, stdoutWriter, szQueue)
	if err != nil {
		log.Printf("terminal: exec session %s finished with: %v", sessionID, err)
	}
}
