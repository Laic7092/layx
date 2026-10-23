// Command server serves the wasm demo. It exists so the browser gets the
// application/wasm MIME type without a separate toolchain.
package main

import (
	"flag"
	"log"
	"net/http"
	"strings"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8088", "listen address")
	dir := flag.String("dir", "examples/wasm/web", "static directory")
	flag.Parse()

	fs := http.FileServer(http.Dir(*dir))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".wasm"):
			w.Header().Set("Content-Type", "application/wasm")
		case strings.HasSuffix(r.URL.Path, ".font"):
			w.Header().Set("Content-Type", "font/ttf")
		}
		w.Header().Set("Cache-Control", "no-cache")
		fs.ServeHTTP(w, r)
	})

	log.Printf("layx wasm demo on http://%s/ (dir %s)", *addr, *dir)
	log.Fatal(http.ListenAndServe(*addr, handler))
}
