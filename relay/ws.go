package relay

import (
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
)

// wsPeer adapts a gorilla/websocket connection to the Peer interface.
// Sends are serialized so concurrent hub broadcasts never interleave frames.
type wsPeer struct {
	mu   sync.Mutex
	conn *websocket.Conn
	id   string
}

var peerSeq int64

func nextPeerID() string {
	return fmt.Sprintf("sock-%d", atomic.AddInt64(&peerSeq, 1))
}

func newWSPeer(conn *websocket.Conn) *wsPeer {
	return &wsPeer{conn: conn, id: nextPeerID()}
}

func (p *wsPeer) ID() string { return p.id }

func (p *wsPeer) Send(b []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.conn.WriteMessage(websocket.TextMessage, b)
}

// wsHub couples a Hub with websocket upgrade handling.
type wsHub struct {
	hub  *Hub
	log  Logger
	upgrader websocket.Upgrader
}

// Logger is where transport-level events go. nil = log package.
type Logger interface {
	Printf(format string, v ...any)
}

type stdLogger struct{}

func (stdLogger) Printf(f string, v ...any) { log.Printf(f, v...) }

// NewMux builds the HTTP handler serving /ws for the given hub.
// A nil logger uses the standard log package.
func NewMux(h *Hub, lg Logger) http.Handler {
	w := &wsHub{
		hub: h,
		log: lg,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin: func(r *http.Request) bool {
				return true // token auth is the gate; browsers cannot set Origin on WS
			},
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", w.serveWS)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	return mux
}

func (w *wsHub) serveWS(rw http.ResponseWriter, r *http.Request) {
	conn, err := w.upgrader.Upgrade(rw, r, nil)
	if err != nil {
		return
	}
	p := newWSPeer(conn)
	defer func() {
		w.hub.Unregister(p)
		conn.Close()
	}()

	// First frame must be register.
	var reg Envelope
	conn.SetReadLimit(64 * 1024 * 1024) // voice blobs can be large
	if err := conn.ReadJSON(&reg); err != nil {
		return
	}
	if err := w.hub.Register(p, reg); err != nil {
		p.Send(marshal(Envelope{Type: "error", Error: err.Error()}))
		return
	}

	for {
		var e Envelope
		if err := conn.ReadJSON(&e); err != nil {
			return
		}
		if e.Type == "ping" {
			p.Send(marshal(Envelope{Type: "pong"}))
			continue
		}
		if err := w.hub.Route(p, e); err != nil {
			p.Send(marshal(Envelope{Type: "error", Error: err.Error()}))
		}
	}
}

// MarshalEnvelope is exported for other packages (e.g. CLI smoke tests).
func MarshalEnvelope(e Envelope) []byte { return marshal(e) }
