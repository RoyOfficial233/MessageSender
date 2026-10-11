// smoke-teacher simulates a teacher console: connects, registers, waits for
// presence, sends a TTS broadcast, and verifies reports + acks arrive.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/gorilla/websocket"

	"github.com/RoyOfficial233/messagesender/relay"
)

func fatal(format string, v ...any) {
	fmt.Printf("SMOKE-FAIL: "+format+"\n", v...)
	os.Exit(1)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func main() {
	url := os.Getenv("MS_URL")
	if url == "" {
		url = "ws://127.0.0.1:8080/ws"
	}
	token := os.Getenv("MS_TOKEN")
	if token == "" {
		token = "test123"
	}

	d := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	c, _, err := d.Dial(url, nil)
	if err != nil {
		fatal("dial: %v", err)
	}
	defer c.Close()

	reg := relay.Envelope{Type: "register", Role: "teacher", ClientID: "smoke-teacher", Token: token}
	if err := c.WriteJSON(reg); err != nil {
		fatal("register: %v", err)
	}

	// wait for presence with >= 2 clients
	var ids []string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var e relay.Envelope
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		if err := c.ReadJSON(&e); err != nil {
			fatal("read presence: %v", err)
		}
		if e.Type == "presence" && len(e.Clients) >= 2 {
			for _, ci := range e.Clients {
				ids = append(ids, ci.ID)
			}
			break
		}
	}
	if len(ids) < 2 {
		fatal("expected 2 clients online, got %v", ids)
	}
	fmt.Printf("SMOKE: presence ok %v\n", ids)

	// send TTS broadcast
	msgID := "smoke-1"
	err = c.WriteJSON(relay.Envelope{
		Type:   "msg",
		MsgID:  msgID,
		Target: []string{"*"},
		Payload: json.RawMessage(`{"kind":"tts","text":"冒烟测试：请打开课本","voice":"","rate":0,"volume":100}`),
	})
	if err != nil {
		fatal("send msg: %v", err)
	}

	// Reader goroutine: gorilla treats a read timeout as fatal, so do blocking
	// reads in the background and multiplex via channel. Clear any deadline
	// left over from the presence wait above.
	c.SetReadDeadline(time.Time{})
	type readResult struct {
		e   relay.Envelope
		err error
	}
	reads := make(chan readResult, 16)
	go func() {
		for {
			var e relay.Envelope
			err := c.ReadJSON(&e)
			reads <- readResult{e, err}
			if err != nil {
				return
			}
		}
	}()

	// expect report delivered=2 + acks (received, spoken) from both
	gotReport := false
	ackStates := map[string]map[string]bool{"pc-01": {}, "pc-02": {}}
	deadline = time.Now().Add(30 * time.Second) // SAPI speech takes a few sec
	for time.Now().Before(deadline) {
		var r readResult
		select {
		case r = <-reads:
		case <-time.After(500 * time.Millisecond):
			continue
		}
		if r.err != nil {
			fatal("read: %v", r.err)
		}
		e := r.e
		switch e.Type {
		case "report":
			if e.Report != nil && e.Report.MsgID == msgID {
				del := e.Report.Delivered
				if !contains(del, "pc-01") || !contains(del, "pc-02") {
					fatal("report delivered = %v, want pc-01+pc-02", del)
				}
				gotReport = true
				fmt.Printf("SMOKE: report delivered=%v missed=%v\n", del, e.Report.Missed)
			}
		case "ack":
			if e.ACK != nil && e.ACK.MsgID == msgID {
				if m, ok := ackStates[e.ACK.From]; ok {
					m[e.ACK.State] = true
				}
				spoken01 := ackStates["pc-01"]["spoken"]
				spoken02 := ackStates["pc-02"]["spoken"]
				if gotReport && spoken01 && spoken02 {
					fmt.Println("SMOKE: acks ok — pc-01 & pc-02 both received+spoken")
					fmt.Println("SMOKE-PASS")
					return
				}
			}
		}
	}
	fatal("timeout: report=%v acks=%v", gotReport, ackStates)
}
