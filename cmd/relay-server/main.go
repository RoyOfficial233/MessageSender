// relay-server runs the classroom message relay: WebSocket hub + optional
// static hosting of the built web frontend.
//
// Usage:
//
//	relay-server -addr :8080 -token mysecret -dist ./web/dist
package main

import (
	"flag"
	"log"
	"net/http"
	"strings"

	"github.com/RoyOfficial233/messagesender/relay"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"


func main() {
	addr := flag.String("addr", ":8080", "listen address")
	token := flag.String("token", "", "shared auth token (empty disables auth)")
	dist := flag.String("dist", "", "optional directory of built frontend to serve at /")
	flag.Parse()

	// API mux: /ws (websocket) and /healthz.
	api := relay.NewMux(relay.NewHub(*token), nil)

	root := http.Handler(api)
	if *dist != "" {
		fs := http.FileServer(http.Dir(*dist))
		root = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// SPA fallback: paths without a file extension serve index.html
			p := r.URL.Path
			if p != "/" && !strings.Contains(p[1:], ".") {
				r.URL.Path = "/"
			}
			fs.ServeHTTP(w, r)
		})
	}

	mux := http.NewServeMux()
	mux.Handle("/ws", api)
	mux.Handle("/healthz", api)
	mux.Handle("/", root)

	log.Printf("relay-server %s listening on %s (token=%s, dist=%q)", version, *addr, mask(*token), *dist)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}

func mask(t string) string {
	if t == "" {
		return "off"
	}
	if len(t) <= 4 {
		return "****"
	}
	return t[:2] + "****" + t[len(t)-2:]
}
