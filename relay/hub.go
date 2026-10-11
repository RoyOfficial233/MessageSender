// Package relay implements the message hub for the teacher->classroom PC
// messaging system. The hub is transport-agnostic: peers are anything that
// can receive bytes (WebSocket conns in production, fakes in tests).
package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Errors returned by Register/Route.
var (
	ErrBadToken    = errors.New("invalid token")
	ErrNotTeacher  = errors.New("only teachers may send messages")
	ErrBadEnvelope = errors.New("malformed envelope")
)

// ClientInfo describes a controlled classroom PC in a presence broadcast.
type ClientInfo struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Online bool     `json:"online"`
	Voices []string `json:"voices,omitempty"`
}

// ACK is a delivery/spoken confirmation sent by a client back to teachers.
type ACK struct {
	MsgID string `json:"msgId"`
	From  string `json:"from"`
	State string `json:"state"` // received | displayed | spoken
}

// Report summarizes where one routed message actually went.
type Report struct {
	MsgID     string   `json:"msgId"`
	Delivered []string `json:"delivered"`
	Missed    []string `json:"missed"`
}

// Envelope is the single wire format for every message on the socket.
type Envelope struct {
	Type     string          `json:"type"` // register | presence | msg | ack | report | replaced
	Role     string          `json:"role,omitempty"`
	ClientID string          `json:"clientId,omitempty"`
	Name     string          `json:"name,omitempty"`
	Token    string          `json:"token,omitempty"`
	Voices   []string        `json:"voices,omitempty"`
	Clients  []ClientInfo    `json:"clients,omitempty"`
	MsgID    string          `json:"msgId,omitempty"`
	Target   []string        `json:"target,omitempty"` // client ids, "*" = all
	Payload  json.RawMessage `json:"payload,omitempty"`
	ACK      *ACK            `json:"ack,omitempty"`
	Report   *Report         `json:"report,omitempty"`
	Error    string          `json:"error,omitempty"`
}

// Peer is one connected socket.
type Peer interface {
	ID() string
	Send(b []byte) error
}

// Hub tracks teachers and classroom clients and routes messages between them.
type Hub struct {
	mu       sync.RWMutex
	token    string
	teachers map[string]Peer // socket id -> peer
	clients  map[string]Peer // client id -> current peer
	meta     map[string]ClientInfo // client id -> last known metadata
}

// NewHub creates a hub. token == "" disables authentication.
func NewHub(token string) *Hub {
	return &Hub{
		token:    token,
		teachers: make(map[string]Peer),
		clients:  make(map[string]Peer),
		meta:     make(map[string]ClientInfo),
	}
}

var msgSeq int64

func newMsgID() string {
	return fmt.Sprintf("m%d-%d", time.Now().UnixNano(), atomic.AddInt64(&msgSeq, 1))
}

func marshal(e Envelope) []byte {
	b, err := json.Marshal(e)
	if err != nil {
		return []byte(`{"type":"error"}`)
	}
	return b
}

// Register handles a register envelope from a newly connected peer.
func (h *Hub) Register(p Peer, e Envelope) error {
	if e.Type != "register" {
		return ErrBadEnvelope
	}
	if h.token != "" && e.Token != h.token {
		return ErrBadToken
	}
	switch e.Role {
	case "teacher":
		h.mu.Lock()
		h.teachers[p.ID()] = p
		h.mu.Unlock()
		h.sendPresence(p)
		return nil
	case "client":
		h.mu.Lock()
		h.meta[e.ClientID] = ClientInfo{ID: e.ClientID, Name: e.Name, Online: true, Voices: e.Voices}
		if old, ok := h.clients[e.ClientID]; ok {
			old.Send(marshal(Envelope{Type: "replaced"}))
			delete(h.clients, e.ClientID)
		}
		h.clients[e.ClientID] = p
		h.mu.Unlock()
		h.broadcastPresence()
		return nil
	}
	return ErrBadEnvelope
}

// Unregister drops a disconnected peer and updates presence if needed.
func (h *Hub) Unregister(p Peer) {
	h.mu.Lock()
	delete(h.teachers, p.ID())
	removed := false
	for id, cp := range h.clients {
		if cp.ID() == p.ID() {
			delete(h.clients, id)
			removed = true
		}
	}
	h.mu.Unlock()
	if removed {
		h.broadcastPresence()
	}
}

func (h *Hub) snapshot() []ClientInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]ClientInfo, 0, len(h.clients))
	for id := range h.clients {
		if m, ok := h.meta[id]; ok {
			out = append(out, m)
		} else {
			out = append(out, ClientInfo{ID: id, Online: true})
		}
	}
	return out
}

// sendPresence pushes the current client list to one teacher.
// Client metadata (name/voices) is filled by the caller-facing Register via
// clientInfos; snapshot() only knows ids, so presence carries id+online.
func (h *Hub) sendPresence(p Peer) {
	p.Send(marshal(Envelope{Type: "presence", Clients: h.snapshot()}))
}

func (h *Hub) broadcastPresence() {
	h.mu.RLock()
	teachers := make([]Peer, 0, len(h.teachers))
	for _, p := range h.teachers {
		teachers = append(teachers, p)
	}
	h.mu.RUnlock()
	env := marshal(Envelope{Type: "presence", Clients: h.snapshot()})
	for _, p := range teachers {
		p.Send(env)
	}
}

// Route dispatches a message or ack envelope from an already-registered peer.
func (h *Hub) Route(from Peer, e Envelope) error {
	switch e.Type {
	case "msg":
		h.mu.RLock()
		_, isTeacher := h.teachers[from.ID()]
		h.mu.RUnlock()
		if !isTeacher {
			return ErrNotTeacher
		}
		if len(e.MsgID) == 0 {
			e.MsgID = newMsgID()
		}
		var delivered, missed []string
		h.mu.RLock()
		wantAll := false
		for _, t := range e.Target {
			if t == "*" {
				wantAll = true
			}
		}
		sent := make(map[string]bool)
		if wantAll {
			for id, p := range h.clients {
				if err := p.Send(marshal(e)); err == nil {
					delivered = append(delivered, id)
					sent[id] = true
				}
			}
		}
		for _, t := range e.Target {
			if t == "*" || sent[t] {
				continue
			}
			if p, ok := h.clients[t]; ok {
				if err := p.Send(marshal(e)); err == nil {
					delivered = append(delivered, t)
					sent[t] = true
				} else {
					missed = append(missed, t)
				}
			} else {
				missed = append(missed, t)
			}
		}
		h.mu.RUnlock()
		from.Send(marshal(Envelope{Type: "report", Report: &Report{
			MsgID: e.MsgID, Delivered: delivered, Missed: missed,
		}}))
		return nil
	case "ack":
		if e.ACK == nil {
			return ErrBadEnvelope
		}
		env := marshal(Envelope{Type: "ack", ACK: e.ACK})
		h.mu.RLock()
		teachers := make([]Peer, 0, len(h.teachers))
		for _, p := range h.teachers {
			teachers = append(teachers, p)
		}
		h.mu.RUnlock()
		for _, p := range teachers {
			p.Send(env)
		}
		return nil
	}
	return ErrBadEnvelope
}
