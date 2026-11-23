package api

import (
	"log"
	"sync"

	"github.com/abhi-vmlinuz/nexus-framework/nexus-engine/internal/k8s"
	"github.com/gorilla/websocket"
)

// WSBackend is the web-app mode of the session terminal: the API proxies the
// PTY to another engine and injects that engine's API key server-side.
//
// The zero value has a nil client, so CanServe() is false and the PTY mode
// stays the default.
type WSBackend struct {
	// Client dials the upstream engine. Injected, so the proxy is testable
	// without any network.
	Client k8s.WSSessionClient
}

// CanServe reports whether web-app mode is enabled.
func (b WSBackend) CanServe() bool { return b.Client != nil }

// clientToUpstream relays everything the browser sends to the upstream engine.
// Terminal control frames (resize) are forwarded as-is: the engine's PTY mode
// parses them and the SSH-alike mode will need to as well.
func (b WSBackend) clientToUpstream(ws *websocket.Conn, up k8s.WSUpstream) {
	for {
		mt, data, err := ws.ReadMessage()
		if err != nil {
			return
		}
		if mt == websocket.CloseMessage {
			return
		}
		if err := up.Send(data); err != nil {
			log.Printf("ws proxy: upstream write failed: %v", err)
			return
		}
	}
}

// upstreamToClient relays everything the upstream sends back to the browser.
// The engine's PTY writes binary messages; the SSH-alike will write whatever it
// wants — the browser handles both.
func (b WSBackend) upstreamToClient(up k8s.WSUpstream, ws *websocket.Conn) {
	for {
		data, err := up.Receive()
		if err != nil {
			return
		}
		if len(data) == 0 {
			continue
		}
		if err := ws.WriteMessage(websocket.BinaryMessage, data); err != nil {
			log.Printf("ws proxy: browser write failed: %v", err)
			return
		}
	}
}

// Pump runs the bidirectional relay until either side closes.
func (b WSBackend) Pump(ws *websocket.Conn, up k8s.WSUpstream) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); b.clientToUpstream(ws, up) }()
	go func() { defer wg.Done(); b.upstreamToClient(up, ws) }()
	wg.Wait()
}
