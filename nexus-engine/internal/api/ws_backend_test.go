package api_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abhi-vmlinuz/nexus-framework/nexus-engine/internal/api"
	"github.com/abhi-vmlinuz/nexus-framework/nexus-engine/internal/k8s"
	"github.com/gorilla/websocket"
)

// fakeUpstream is a k8s.WSSessionClient whose "upstream" is in-process.
type fakeUpstream struct {
	sendMu  sync.Mutex
	sent    [][]byte
	readCh  chan []byte
	closeCh chan struct{}
}

func newFakeUpstream() *fakeUpstream {
	return &fakeUpstream{readCh: make(chan []byte, 8), closeCh: make(chan struct{})}
}

func (f *fakeUpstream) Send(data []byte) error {
	f.sendMu.Lock()
	defer f.sendMu.Unlock()
	f.sent = append(f.sent, append([]byte(nil), data...))
	return nil
}

func (f *fakeUpstream) Receive() ([]byte, error) {
	select {
	case data := <-f.readCh:
		return data, nil
	case <-f.closeCh:
		return nil, io.EOF
	}
}

func (f *fakeUpstream) Close() error { close(f.closeCh); return nil }

func (f *fakeUpstream) sentBytes() [][]byte {
	f.sendMu.Lock()
	defer f.sendMu.Unlock()
	return append([][]byte(nil), f.sent...)
}

type fakeClient struct {
	upstream *fakeUpstream
	dialed   chan string
}

func newFakeClient() *fakeClient {
	return &fakeClient{upstream: newFakeUpstream(), dialed: make(chan string, 4)}
}

func (c *fakeClient) OpenTerminal(_ context.Context, sessionID string) (k8s.WSUpstream, error) {
	select {
	case c.dialed <- sessionID:
	default:
	}
	return c.upstream, nil
}

// pumpServer runs the backend's relay against a real gorilla WebSocket client.
// onConnect fires after the handshake, on the server side.
func pumpServer(t *testing.T, backend api.WSBackend) (*httptest.Server, chan string) {
	t.Helper()
	dialed := make(chan string, 4)
	up := backend.Client
	_ = up
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := func() *websocket.Upgrader {
			u := &websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
			return u
		}().Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		sessionID := strings.TrimPrefix(r.URL.Path, "/api/v1/sessions/")
		sessionID = strings.TrimSuffix(sessionID, "/terminal")
		dialed <- sessionID

		upstream, err := backend.Client.OpenTerminal(r.Context(), sessionID)
		if err != nil {
			conn.Close()
			return
		}
		defer upstream.Close()
		backend.Pump(conn, upstream)
	}))
	t.Cleanup(srv.Close)
	return srv, dialed
}

func dialPump(t *testing.T, srv *httptest.Server, sessionID string) *websocket.Conn {
	t.Helper()
	url := strings.Replace(srv.URL, "http://", "ws://", 1) +
		"/api/v1/sessions/" + sessionID + "/terminal"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn
}

func waitForSent(t *testing.T, f *fakeUpstream, want []byte) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, got := range f.sentBytes() {
			if bytes.Contains(got, want) {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("message %q did not reach upstream in 3s; sent=%q", want, f.sentBytes())
}

func readClientMessage(t *testing.T, conn *websocket.Conn) []byte {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	mt, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read from browser: %v", err)
	}
	if mt != websocket.BinaryMessage {
		t.Errorf("got message type %d, want Binary", mt)
	}
	return data
}

func TestPumpRelaysBrowserToUpstreamAndBack(t *testing.T) {
	fake := newFakeClient()
	backend := api.WSBackend{Client: fake}
	srv, dialed := pumpServer(t, backend)

	conn := dialPump(t, srv, "sess-1")
	defer conn.Close()

	select {
	case id := <-dialed:
		if id != "sess-1" {
			t.Errorf("dialed session %q, want sess-1", id)
		}
	default:
		t.Fatal("upstream was not dialed")
	}

	// Browser -> upstream.
	if err := conn.WriteMessage(websocket.TextMessage, []byte("echo hello\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitForSent(t, fake.upstream, []byte("echo hello\n"))

	// Upstream -> browser.
	fake.upstream.readCh <- []byte("hello\n")
	if got := readClientMessage(t, conn); !bytes.Contains(got, []byte("hello\n")) {
		t.Errorf("browser got %q, want it to contain %q", got, "hello\n")
	}
}

func TestPumpRelaysBinaryBothWays(t *testing.T) {
	fake := newFakeClient()
	backend := api.WSBackend{Client: fake}
	srv, _ := pumpServer(t, backend)

	conn := dialPump(t, srv, "sess-2")
	defer conn.Close()

	blob := []byte{0x00, 0x01, 0xff, 0xfe}
	if err := conn.WriteMessage(websocket.BinaryMessage, blob); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitForSent(t, fake.upstream, blob)

	fake.upstream.readCh <- blob
	if got := readClientMessage(t, conn); !bytes.Equal(got, blob) {
		t.Errorf("browser got %v, want %v", got, blob)
	}
}

func TestEngineTerminalDialerInjectsAPIKey(t *testing.T) {
	var gotAuth string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		conn, err := func() *websocket.Upgrader {
			u := &websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
			return u
		}().Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			mt, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(mt, data); err != nil {
				return
			}
		}
	}))
	defer up.Close()

	dialer := k8s.NewEngineWSSessionClient(up.URL, "sekret")
	upstream, err := dialer.OpenTerminal(context.Background(), "sess-x")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer upstream.Close()

	if gotAuth != "Bearer sekret" {
		t.Errorf("Authorization header = %q, want Bearer sekret", gotAuth)
	}

	if err := upstream.Send([]byte("ping")); err != nil {
		t.Fatalf("send: %v", err)
	}
	data, err := upstream.Receive()
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if !bytes.Equal(data, []byte("ping")) {
		t.Errorf("echo got %q, want ping", data)
	}
}
