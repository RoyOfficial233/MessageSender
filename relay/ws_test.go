package relay_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/RoyOfficial233/messagesender/relay"
)

// setup spins up a real relay HTTP server and returns its WS URL.
func setup(t *testing.T, token string) string {
	t.Helper()
	mux := relay.NewMux(relay.NewHub(token), nil)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
}

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	d := websocket.Dialer{HandshakeTimeout: 3 * time.Second}
	c, _, err := d.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func sendEnv(t *testing.T, c *websocket.Conn, e relay.Envelope) {
	t.Helper()
	if err := c.WriteJSON(e); err != nil {
		t.Fatalf("write %s: %v", e.Type, err)
	}
}

// recvUntil reads envelopes until pred matches or timeout.
func recvUntil(t *testing.T, c *websocket.Conn, timeout time.Duration, pred func(relay.Envelope) bool) relay.Envelope {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(timeout))
	for {
		var e relay.Envelope
		if err := c.ReadJSON(&e); err != nil {
			t.Fatalf("read: %v", err)
		}
		if pred(e) {
			return e
		}
	}
}

func TestWsEndToEnd(t *testing.T) {
	url := setup(t, "")

	teacher := dial(t, url)
	pc := dial(t, url)

	sendEnv(t, teacher, relay.Envelope{Type: "register", Role: "teacher", ClientID: "t1"})
	sendEnv(t, pc, relay.Envelope{Type: "register", Role: "client", ClientID: "pc-01", Name: "一号机房", Voices: []string{"zh-CN"}})

	// teacher sees pc-01 online
	p := recvUntil(t, teacher, 3*time.Second, func(e relay.Envelope) bool {
		return e.Type == "presence" && len(e.Clients) == 1 && e.Clients[0].ID == "pc-01"
	})
	if p.Clients[0].Name != "一号机房" {
		t.Errorf("name = %q", p.Clients[0].Name)
	}

	// teacher broadcasts a TTS message
	sendEnv(t, teacher, relay.Envelope{
		Type:    "msg",
		MsgID:   "m-1",
		Target:  []string{"*"},
		Payload: json.RawMessage(`{"kind":"tts","text":"请打开课本第12页","voice":"zh-CN","volume":1}`),
	})

	got := recvUntil(t, pc, 3*time.Second, func(e relay.Envelope) bool { return e.Type == "msg" })
	var pay map[string]any
	if err := json.Unmarshal(got.Payload, &pay); err != nil {
		t.Fatal(err)
	}
	if pay["text"] != "请打开课本第12页" {
		t.Errorf("payload = %v", pay)
	}
	if got.MsgID != "m-1" {
		t.Errorf("msgId = %q", got.MsgID)
	}

	// client acks
	sendEnv(t, pc, relay.Envelope{Type: "ack", ACK: &relay.ACK{MsgID: "m-1", From: "pc-01", State: "spoken"}})

	rep := recvUntil(t, teacher, 3*time.Second, func(e relay.Envelope) bool { return e.Type == "report" })
	if len(rep.Report.Delivered) != 1 || rep.Report.Delivered[0] != "pc-01" {
		t.Errorf("report = %+v", rep.Report)
	}
	ack := recvUntil(t, teacher, 3*time.Second, func(e relay.Envelope) bool { return e.Type == "ack" })
	if ack.ACK.State != "spoken" {
		t.Errorf("ack = %+v", ack.ACK)
	}
}

func TestWsRegisterGate(t *testing.T) {
	url := setup(t, "sekrit")
	c := dial(t, url)
	sendEnv(t, c, relay.Envelope{Type: "register", Role: "client", ClientID: "c1", Token: "nope"})
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, raw, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("expected error envelope before close, got: %v", err)
	}
	if !strings.Contains(string(raw), "invalid token") && !strings.Contains(string(raw), "error") {
		t.Errorf("unexpected reply: %s", raw)
	}
}

func TestWsDisconnectUpdatesPresence(t *testing.T) {
	url := setup(t, "")
	teacher := dial(t, url)
	sendEnv(t, teacher, relay.Envelope{Type: "register", Role: "teacher", ClientID: "t1"})

	pc := dial(t, url)
	sendEnv(t, pc, relay.Envelope{Type: "register", Role: "client", ClientID: "pc-01"})
	recvUntil(t, teacher, 3*time.Second, func(e relay.Envelope) bool {
		return e.Type == "presence" && len(e.Clients) == 1
	})

	pc.Close()
	recvUntil(t, teacher, 3*time.Second, func(e relay.Envelope) bool {
		return e.Type == "presence" && len(e.Clients) == 0
	})
}
