// relay-client runs on each classroom PC. It connects to the relay server,
// registers as a controlled client, and executes incoming messages:
//
//   kind=tts    -> speak text via Windows SAPI (voice/rate/volume honored)
//   kind=audio  -> play the base64 WAV payload through SoundPlayer
//   kind=text   -> show a popup banner (auto-dismiss)
//
// Every stage is acked back to the teacher (received/displayed/spoken).
//
// Usage:
//
//	relay-client -url ws://teacher-pc:8080/ws -id pc-01 -name "一号机房" -token sekrit
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"log"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"

	"github.com/RoyOfficial233/messagesender/relay"
)

type msgPayload struct {
	Kind   string `json:"kind"` // tts | audio | text
	Text   string `json:"text,omitempty"`
	B64    string `json:"b64,omitempty"`
	Voice  string `json:"voice,omitempty"`
	Rate   int    `json:"rate,omitempty"`   // -10..10
	Volume int    `json:"volume,omitempty"` // 0..100
	Stay   int    `json:"stay,omitempty"`   // banner seconds
}

var (
	wsURL  = flag.String("url", "ws://127.0.0.1:8080/ws", "relay server websocket url")
	client = flag.String("id", "", "client id (default: hostname)")
	name   = flag.String("name", "", "human-readable name shown to teacher")
	token  = flag.String("token", "", "shared auth token")
	proxy  = flag.String("proxy", "auto",
		`proxy for reaching the relay: "none" = direct; "auto"/"env" = read ALL_PROXY/HTTPS_PROXY/HTTP_PROXY; or an explicit url like http://127.0.0.1:7890, socks5://127.0.0.1:7891, socks5h://…`)
	quiet  = flag.Bool("quiet", false, "log to a file instead of console")
)

func main() {
	flag.Parse()
	id := *client
	if id == "" {
		h, err := os.Hostname()
		if err != nil {
			log.Fatal(err)
		}
		id = h
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	voices := listVoices() // PowerShell is ~1s slow; do once before connecting
	if len(voices) > 0 {
		log.Printf("TTS voices: %v", voices)
	} else {
		log.Printf("no TTS voices detected (System.Speech unavailable?)")
	}

	dialer, err := buildDialer(*proxy, 10*time.Second, os.Getenv)
	if err != nil {
		log.Fatalf("-proxy: %v", err)
	}
	if resolved, _ := resolveProxySpec(*proxy, os.Getenv); resolved != "" {
		log.Printf("using proxy %s", resolved)
	}

	c := &app{id: id, name: *name, token: *token, voices: voices, dialer: dialer}
	c.run(ctx)
}

type app struct {
	id, name, token string
	voices          []string
	dialer          *websocket.Dialer
}

func (a *app) run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		if err := a.session(ctx); err != nil {
			log.Printf("session: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (a *app) session(ctx context.Context) error {
	u, err := url.Parse(*wsURL)
	if err != nil {
		return err
	}
	conn, _, err := a.dialer.DialContext(ctx, u.String(), nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	log.Printf("connected to %s as %q", u.String(), a.id)

	reg := relay.Envelope{Type: "register", Role: "client", ClientID: a.id, Name: a.name, Token: a.token, Voices: a.voices}
	if reg.Name == "" {
		reg.Name = a.id
	}
	if err := conn.WriteJSON(reg); err != nil {
		return err
	}

	pingCtx, cancelPing := context.WithCancel(ctx)
	defer cancelPing()
	go func() { // keepalive
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-pingCtx.Done():
				return
			case <-t.C:
				conn.WriteJSON(relay.Envelope{Type: "ping"})
			}
		}
	}()

	for {
		var e relay.Envelope
		if err := conn.ReadJSON(&e); err != nil {
			return err
		}
		switch e.Type {
		case "msg":
			go a.handle(conn, e)
		case "replaced":
			log.Printf("another socket took over id %s, exiting session", a.id)
			return nil
		case "pong", "presence":
		}
	}
}

func (a *app) handle(conn *websocket.Conn, e relay.Envelope) {
	ack := func(state string) {
		conn.WriteJSON(relay.Envelope{Type: "ack", ACK: &relay.ACK{MsgID: e.MsgID, From: a.id, State: state}})
	}
	ack("received")

	var p msgPayload
	if len(e.Payload) > 0 {
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			log.Printf("msg %s: bad payload: %v", e.MsgID, err)
			return
		}
	}
	log.Printf("msg %s: kind=%s", e.MsgID, p.Kind)

	switch p.Kind {
	case "tts":
		if err := speak(p.Text, p.Voice, p.Rate, p.Volume); err != nil {
			log.Printf("tts: %v", err)
			return
		}
		ack("spoken")
	case "audio":
		wav, err := base64.StdEncoding.DecodeString(p.B64)
		if err != nil {
			log.Printf("audio: bad base64: %v", err)
			return
		}
		if err := playWav(wav); err != nil {
			log.Printf("audio: %v", err)
			return
		}
		ack("spoken")
	case "text":
		stay := p.Stay
		if stay <= 0 {
			stay = 10
		}
		banner(p.Text, stay)
		ack("displayed")
	default:
		log.Printf("msg %s: unknown kind %q", e.MsgID, p.Kind)
	}
}

// ---- Windows helpers via PowerShell (UTF-8 safe: script file + base64 text) ----

func runPS(script string) (string, error) {
	dir, err := filepath.Abs(".")
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "ms-*.ps1")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	// UTF-8 BOM so Windows PowerShell 5.1 parses non-ASCII literals correctly.
	if _, err := f.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		f.Close()
		return "", err
	}
	if _, err := f.WriteString(script); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", f.Name())
	out, err := cmd.Output()
	return string(out), err
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func psString(s string) string {
	return "$t=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('" + b64(s) + "'))"
}

func listVoices() []string {
	out, err := runPS(`
Add-Type -AssemblyName System.Speech
$s = New-Object System.Speech.Synthesis.SpeechSynthesizer
$s.GetInstalledVoices() | ForEach-Object { $_.VoiceInfo.Name }
`)
	if err != nil {
		return nil
	}
	var vs []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			vs = append(vs, l)
		}
	}
	return vs
}

func speak(text, voice string, rate, volume int) error {
	if volume <= 0 {
		volume = 100
	}
	var sel strings.Builder
	sel.WriteString(psString(text))
	if voice != "" {
		vs := strings.ReplaceAll(voice, "'", "''")
		sel.WriteString("\n$vs='" + vs + "'\n")
	} else {
		sel.WriteString("\n$vs=''\n")
	}
	script := `
Add-Type -AssemblyName System.Speech
` + sel.String() + `
$s = New-Object System.Speech.Synthesis.SpeechSynthesizer
try { if ($vs -ne '') { $s.SelectVoice($vs) } } catch {}
$s.Rate = ` + itoa(rate) + `
$s.Volume = ` + itoa(volume) + `
$s.Speak($t)
`
	_, err := runPS(script)
	return err
}

func playWav(wav []byte) error {
	dir, err := filepath.Abs(".")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "ms-*.wav")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(wav); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	p := strings.ReplaceAll(tmp.Name(), `'`, `''`)
	_, err = runPS(`
(New-Object Media.SoundPlayer '` + p + `').PlaySync()
`)
	return err
}

func banner(text string, staySec int) {
	script := `
Add-Type -AssemblyName System.Windows.Forms
` + psString(text) + `
$sh = New-Object -ComObject WScript.Shell
$sh.Popup($t, ` + itoa(staySec) + `, '教师通知', 64) | Out-Null
`
	if _, err := runPS(script); err != nil {
		log.Printf("banner: %v", err)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return strings.Trim(string(b), `"`)
}
