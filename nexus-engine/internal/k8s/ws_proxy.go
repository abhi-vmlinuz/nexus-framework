package k8s

import (
	"context"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
)

// WSSessionClient opens a live WebSocket for a session. It exists so tests can
// substitute a fake, and so the web-app mode of the API can dial a *different*
// engine's terminal endpoint without anything HTTP-specific leaking into the
// proxy.
type WSSessionClient interface {
	OpenTerminal(ctx context.Context, sessionID string) (WSUpstream, error)
}

// WSUpstream is one live connection to a session's terminal.
type WSUpstream interface {
	Send(data []byte) error
	Receive() ([]byte, error)
	Close() error
}

// EngineWSSessionClient dials another engine's terminal endpoint for a
// session. The API key from the engine's rest.Config is injected as an
// Authorization header: the terminal route sits behind RequireAPIKey, and
// browsers cannot put headers on a WebSocket, but this connection is not a
// browser.
type EngineWSSessionClient struct {
	wsURL string
	Key   string
}

// NewEngineWSSessionClient returns a client that dials wsURL. http/https URLs
// are accepted and converted.
func NewEngineWSSessionClient(wsURL, key string) EngineWSSessionClient {
	switch {
	case strings.HasPrefix(wsURL, "http://"):
		wsURL = "ws://" + strings.TrimPrefix(wsURL, "http://")
	case strings.HasPrefix(wsURL, "https://"):
		wsURL = "wss://" + strings.TrimPrefix(wsURL, "https://")
	}
	return EngineWSSessionClient{wsURL: wsURL, Key: key}
}

func (c EngineWSSessionClient) OpenTerminal(ctx context.Context, sessionID string) (WSUpstream, error) {
	url := c.wsURL + "/api/v1/sessions/" + sessionID + "/terminal"
	var headers http.Header
	if c.Key != "" {
		headers = http.Header{}
		headers.Set("Authorization", "Bearer "+c.Key)
	}
	conn, _, err := websocket.DefaultDialer.Dial(url, headers)
	if err != nil {
		return nil, err
	}
	return &engineUpstream{conn: conn}, nil
}

type engineUpstream struct{ conn *websocket.Conn }

func (u *engineUpstream) Send(data []byte) error {
	return u.conn.WriteMessage(websocket.BinaryMessage, data)
}

func (u *engineUpstream) Receive() ([]byte, error) {
	_, data, err := u.conn.ReadMessage()
	return data, err
}

func (u *engineUpstream) Close() error { return u.conn.Close() }

var _ WSSessionClient = EngineWSSessionClient{}
