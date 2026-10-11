package relay

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakePeer is an in-memory Peer used to test the hub without WebSockets.
type fakePeer struct {
	id   string
	mu   sync.Mutex
	sent []json.RawMessage
}

func (p *fakePeer) ID() string { return p.id }

func (p *fakePeer) Send(b []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, append([]byte(nil), b...))
	return nil
}

func (p *fakePeer) raw() []json.RawMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]json.RawMessage(nil), p.sent...)
}

func (p *fakePeer) envelopes(t *testing.T) []Envelope {
	t.Helper()
	out := make([]Envelope, 0, len(p.raw()))
	for _, r := range p.raw() {
		var e Envelope
		if err := json.Unmarshal(r, &e); err != nil {
			t.Fatalf("peer %s: bad envelope %v: %v", p.id, string(r), err)
		}
		out = append(out, e)
	}
	return out
}

// waitFor polls until an envelope matching pred was sent to p, or fails.
func (p *fakePeer) waitFor(t *testing.T, timeout time.Duration, pred func(Envelope) bool) Envelope {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, e := range p.envelopes(t) {
			if pred(e) {
				return e
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("peer %s: timed out waiting for envelope", p.id)
	return Envelope{}
}

func (p *fakePeer) neverGot(t *testing.T, pred func(Envelope) bool) bool {
	t.Helper()
	for _, e := range p.envelopes(t) {
		if pred(e) {
			return false
		}
	}
	return true
}

func regTeacher(id string) Envelope {
	return Envelope{Type: "register", Role: "teacher", ClientID: id, Name: id}
}

func regClient(id string) Envelope {
	return Envelope{Type: "register", Role: "client", ClientID: id, Name: id, Voices: []string{"zh-CN-Standard"}}
}

func TestRegisterClientBroadcastsPresenceToTeachers(t *testing.T) {
	h := NewHub("")
	teacher := &fakePeer{id: "sock-t1"}
	client := &fakePeer{id: "sock-c1"}

	if err := h.Register(teacher, regTeacher("t1")); err != nil {
		t.Fatalf("teacher register: %v", err)
	}
	if err := h.Register(client, regClient("c1")); err != nil {
		t.Fatalf("client register: %v", err)
	}

	e := teacher.waitFor(t, time.Second, func(e Envelope) bool {
		return e.Type == "presence" && len(e.Clients) == 1
	})
	c := e.Clients[0]
	if c.ID != "c1" || !c.Online || c.Name != "c1" {
		t.Errorf("presence entry = %+v", c)
	}
	if len(c.Voices) != 1 || c.Voices[0] != "zh-CN-Standard" {
		t.Errorf("voices not carried: %+v", c.Voices)
	}
}

func TestTeacherRegisterReceivesSnapshot(t *testing.T) {
	h := NewHub("")
	c1 := &fakePeer{id: "s1"}
	if err := h.Register(c1, regClient("c1")); err != nil {
		t.Fatal(err)
	}
	t1 := &fakePeer{id: "s2"}
	if err := h.Register(t1, regTeacher("t1")); err != nil {
		t.Fatal(err)
	}
	e := t1.waitFor(t, time.Second, func(e Envelope) bool {
		return e.Type == "presence" && len(e.Clients) == 1
	})
	if e.Clients[0].ID != "c1" {
		t.Errorf("snapshot = %+v", e.Clients)
	}
}

func TestRouteBroadcastToAllClients(t *testing.T) {
	h := NewHub("")
	teacher := &fakePeer{id: "st"}
	c1 := &fakePeer{id: "s1"}
	c2 := &fakePeer{id: "s2"}
	h.Register(teacher, regTeacher("t1"))
	h.Register(c1, regClient("c1"))
	h.Register(c2, regClient("c2"))

	err := h.Route(teacher, Envelope{
		Type:    "msg",
		MsgID:   "m1",
		Target:  []string{"*"},
		Payload: json.RawMessage(`{"kind":"text","text":"hi"}`),
	})
	if err != nil {
		t.Fatalf("route: %v", err)
	}

	for _, c := range []*fakePeer{c1, c2} {
		e := c.waitFor(t, time.Second, func(e Envelope) bool { return e.Type == "msg" && e.MsgID == "m1" })
		if !strings.Contains(string(e.Payload), "hi") {
			t.Errorf("payload missing on %s: %s", c.id, string(e.Payload))
		}
	}

	r := teacher.waitFor(t, time.Second, func(e Envelope) bool { return e.Type == "report" })
	if r.Report == nil {
		t.Fatal("report envelope missing report body")
	}
	if len(r.Report.Delivered) != 2 || len(r.Report.Missed) != 0 {
		t.Errorf("report = %+v", r.Report)
	}
}

func TestRouteTargetsSpecificClient(t *testing.T) {
	h := NewHub("")
	teacher := &fakePeer{id: "st"}
	c1 := &fakePeer{id: "s1"}
	c2 := &fakePeer{id: "s2"}
	h.Register(teacher, regTeacher("t1"))
	h.Register(c1, regClient("c1"))
	h.Register(c2, regClient("c2"))

	h.Route(teacher, Envelope{Type: "msg", MsgID: "m1", Target: []string{"c2"}, Payload: json.RawMessage(`{}`)})

	c2.waitFor(t, time.Second, func(e Envelope) bool { return e.Type == "msg" })
	if !c1.neverGot(t, func(e Envelope) bool { return e.Type == "msg" }) {
		t.Error("c1 should not receive targeted message for c2")
	}
	r := teacher.waitFor(t, time.Second, func(e Envelope) bool { return e.Type == "report" })
	if len(r.Report.Delivered) != 1 || r.Report.Delivered[0] != "c2" {
		t.Errorf("report = %+v", r.Report)
	}
}

func TestRouteUnknownTargetReportedMissed(t *testing.T) {
	h := NewHub("")
	teacher := &fakePeer{id: "st"}
	h.Register(teacher, regTeacher("t1"))

	h.Route(teacher, Envelope{Type: "msg", MsgID: "m1", Target: []string{"ghost"}, Payload: json.RawMessage(`{}`)})

	r := teacher.waitFor(t, time.Second, func(e Envelope) bool { return e.Type == "report" })
	if len(r.Report.Missed) != 1 || r.Report.Missed[0] != "ghost" {
		t.Errorf("missed = %+v", r.Report.Missed)
	}
}

func TestRouteGeneratesMsgIDWhenMissing(t *testing.T) {
	h := NewHub("")
	teacher := &fakePeer{id: "st"}
	c1 := &fakePeer{id: "s1"}
	h.Register(teacher, regTeacher("t1"))
	h.Register(c1, regClient("c1"))

	h.Route(teacher, Envelope{Type: "msg", Target: []string{"c1"}, Payload: json.RawMessage(`{}`)})

	e := c1.waitFor(t, time.Second, func(e Envelope) bool { return e.Type == "msg" })
	if strings.TrimSpace(e.MsgID) == "" {
		t.Error("hub should fill missing msgId")
	}
}

func TestAckForwardedToTeachers(t *testing.T) {
	h := NewHub("")
	teacher := &fakePeer{id: "st"}
	c1 := &fakePeer{id: "s1"}
	h.Register(teacher, regTeacher("t1"))
	h.Register(c1, regClient("c1"))

	h.Route(c1, Envelope{Type: "ack", ACK: &ACK{MsgID: "m1", From: "c1", State: "displayed"}})

	e := teacher.waitFor(t, time.Second, func(e Envelope) bool { return e.Type == "ack" })
	if e.ACK == nil || e.ACK.MsgID != "m1" || e.ACK.From != "c1" || e.ACK.State != "displayed" {
		t.Errorf("ack = %+v", e.ACK)
	}
}

func TestReconnectReplacesSameClientID(t *testing.T) {
	h := NewHub("")
	teacher := &fakePeer{id: "st"}
	old := &fakePeer{id: "s-old"}
	fresh := &fakePeer{id: "s-new"}
	h.Register(teacher, regTeacher("t1"))
	h.Register(old, regClient("pc1"))
	h.Register(fresh, regClient("pc1"))

	// presence still has exactly one pc1
	e := teacher.waitFor(t, time.Second, func(e Envelope) bool {
		return e.Type == "presence" && len(e.Clients) == 1 && e.Clients[0].ID == "pc1"
	})
	if !e.Clients[0].Online {
		t.Error("replacement client should be online")
	}

	// old socket got a replaced notice
	old.waitFor(t, time.Second, func(e Envelope) bool { return e.Type == "replaced" })

	// routing now goes only to the fresh socket
	h.Route(teacher, Envelope{Type: "msg", MsgID: "m2", Target: []string{"pc1"}, Payload: json.RawMessage(`{}`)})
	fresh.waitFor(t, time.Second, func(e Envelope) bool { return e.Type == "msg" && e.MsgID == "m2" })
	if !old.neverGot(t, func(e Envelope) bool { return e.Type == "msg" }) {
		t.Error("replaced socket must not receive messages")
	}
}

func TestUnregisterBroadcastsPresence(t *testing.T) {
	h := NewHub("")
	teacher := &fakePeer{id: "st"}
	c1 := &fakePeer{id: "s1"}
	h.Register(teacher, regTeacher("t1"))
	h.Register(c1, regClient("c1"))

	h.Unregister(c1)

	teacher.waitFor(t, time.Second, func(e Envelope) bool {
		return e.Type == "presence" && len(e.Clients) == 0
	})
}

func TestRegisterRejectsWrongToken(t *testing.T) {
	h := NewHub("secret")
	p := &fakePeer{id: "s1"}
	if err := h.Register(p, Envelope{Type: "register", Role: "client", ClientID: "c1", Token: "wrong"}); err == nil {
		t.Fatal("expected token rejection")
	}
	ok := &fakePeer{id: "s2"}
	if err := h.Register(ok, Envelope{Type: "register", Role: "client", ClientID: "c1", Token: "secret"}); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
}

func TestRouteRejectsNonTeacherSender(t *testing.T) {
	h := NewHub("")
	teacher := &fakePeer{id: "st"}
	c1 := &fakePeer{id: "s1"}
	c2 := &fakePeer{id: "s2"}
	h.Register(teacher, regTeacher("t1"))
	h.Register(c1, regClient("c1"))
	h.Register(c2, regClient("c2"))

	err := h.Route(c1, Envelope{Type: "msg", Target: []string{"c2"}, Payload: json.RawMessage(`{}`)})
	if err == nil {
		t.Fatal("client should not be allowed to send messages")
	}
	if !c2.neverGot(t, func(e Envelope) bool { return e.Type == "msg" }) {
		t.Error("no message should have been routed")
	}
}
