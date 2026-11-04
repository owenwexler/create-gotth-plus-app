package middleware

import (
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"

	"github.com/andybalholm/brotli"
)

// Brotli at its default level (11) is far too slow for on-the-fly compression.
const brotliLevel = 5

var gzipPool = sync.Pool{New: func() any {
	w, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression)
	return w
}}

var brotliPool = sync.Pool{New: func() any {
	return brotli.NewWriterLevel(io.Discard, brotliLevel)
}}

type encodingWriter interface {
	io.WriteCloser
	Flush() error
	Reset(io.Writer)
}

// Compress compresses responses with brotli or gzip, whichever the client prefers
// (brotli wins when both are accepted). Responses that are already compressed
// (images, fonts, archives), have no body, or answer Range requests are left alone.
func Compress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")

		encoding := negotiate(r.Header.Get("Accept-Encoding"))
		if encoding == "" || r.Header.Get("Range") != "" || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}

		cw := &compressWriter{ResponseWriter: w, encoding: encoding}
		defer cw.close()
		next.ServeHTTP(cw, r)
	})
}

// negotiate returns "br", "gzip", or "" given an Accept-Encoding header.
func negotiate(header string) string {
	var br, gz bool

	for _, part := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		name = strings.ToLower(strings.TrimSpace(name))

		// an explicit q=0 means "not acceptable"
		if q := strings.ReplaceAll(strings.ToLower(params), " ", ""); q == "q=0" || q == "q=0.0" {
			continue
		}

		switch name {
		case "br":
			br = true
		case "gzip":
			gz = true
		}
	}

	switch {
	case br:
		return "br"
	case gz:
		return "gzip"
	}
	return ""
}

type compressWriter struct {
	http.ResponseWriter
	encoding    string
	enc         encodingWriter
	wroteHeader bool
}

func (c *compressWriter) WriteHeader(status int) {
	if c.wroteHeader {
		return
	}
	c.wroteHeader = true

	h := c.Header()
	bodyless := status < 200 || status == http.StatusNoContent || status == http.StatusNotModified

	if !bodyless && h.Get("Content-Encoding") == "" && compressible(h.Get("Content-Type")) {
		h.Set("Content-Encoding", c.encoding)
		h.Del("Content-Length") // length changes once compressed
		h.Del("Accept-Ranges")

		if c.encoding == "br" {
			bw := brotliPool.Get().(*brotli.Writer)
			bw.Reset(c.ResponseWriter)
			c.enc = bw
		} else {
			gw := gzipPool.Get().(*gzip.Writer)
			gw.Reset(c.ResponseWriter)
			c.enc = gw
		}
	}

	c.ResponseWriter.WriteHeader(status)
}

func (c *compressWriter) Write(p []byte) (int, error) {
	if !c.wroteHeader {
		// net/http sniffs the type when none is set; do the same so we can decide
		if c.Header().Get("Content-Type") == "" {
			c.Header().Set("Content-Type", http.DetectContentType(p))
		}
		c.WriteHeader(http.StatusOK)
	}
	if c.enc == nil {
		return c.ResponseWriter.Write(p)
	}
	return c.enc.Write(p)
}

// Flush lets streaming responses (e.g. templ rendering) reach the client early.
// Until WriteHeader has run, the headers (Content-Encoding in particular) are not final, and flushing
// would send them without it. templ flushes its buffers when a component finishes, even an empty one,
// so this must be a no-op before the first write.
func (c *compressWriter) Flush() {
	if !c.wroteHeader {
		return
	}
	if c.enc != nil {
		_ = c.enc.Flush()
	}
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (c *compressWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

func (c *compressWriter) close() {
	if c.enc == nil {
		return
	}
	_ = c.enc.Close()

	switch w := c.enc.(type) {
	case *gzip.Writer:
		gzipPool.Put(w)
	case *brotli.Writer:
		brotliPool.Put(w)
	}
	c.enc = nil
}

func compressible(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}

	switch {
	case strings.HasPrefix(mt, "text/"),
		strings.HasSuffix(mt, "+xml"),
		strings.HasSuffix(mt, "+json"):
		return true
	}

	switch mt {
	case "application/json", "application/javascript", "application/xml", "image/svg+xml":
		return true
	}
	return false
}
