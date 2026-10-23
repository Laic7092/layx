// Command server serves the wasm demo. It exists so the browser gets the
// application/wasm MIME type without a separate toolchain, and so the two big
// assets (layx.wasm, book.font) go over the wire gzip-compressed: the wasm is
// ~7 MB raw but ~1.8 MB gzipped, the font ~5 MB raw but ~2.3 MB gzipped.
package main

import (
	"compress/gzip"
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
		if acceptsGzip(r) && compressible(r.URL.Path) {
			w.Header().Set("Vary", "Accept-Encoding")
			gw := &gzipWriter{ResponseWriter: w}
			defer gw.Close()
			fs.ServeHTTP(gw, r)
			return
		}
		fs.ServeHTTP(w, r)
	})

	log.Printf("layx wasm demo on http://%s/ (dir %s, gzip on)", *addr, *dir)
	log.Fatal(http.ListenAndServe(*addr, handler))
}

// compressible lists the assets worth compressing. PNG/JPEG and friends are
// already compressed, but the demo only ships wasm, font, JS and HTML.
func compressible(path string) bool {
	if path == "/" { // index.html
		return true
	}
	for _, ext := range []string{".wasm", ".font", ".js", ".html", ".css", ".mjs"} {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}

func acceptsGzip(r *http.Request) bool {
	for _, enc := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		if strings.TrimSpace(strings.SplitN(enc, ";", 2)[0]) == "gzip" {
			return true
		}
	}
	return false
}

// gzipWriter strips Content-Length, which http.FileServer sets from the
// uncompressed file size; the gzip stream does not match it.
type gzipWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	wroteHeader bool
}

func (w *gzipWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	h := w.Header()
	h.Del("Content-Length")
	h.Set("Content-Encoding", "gzip")
	w.ResponseWriter.WriteHeader(code)
}

func (w *gzipWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.gz == nil {
		var err error
		w.gz, err = gzip.NewWriterLevel(w.ResponseWriter, gzip.DefaultCompression)
		if err != nil {
			return w.ResponseWriter.Write(b)
		}
	}
	return w.gz.Write(b)
}

func (w *gzipWriter) Close() error {
	if w.gz == nil {
		return nil
	}
	return w.gz.Close()
}
