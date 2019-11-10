// Package gowebcompress applies top compression hueristics to
// offer a dynamic (for APIs) and Static http middleware to
// accelerate your server.
package gowebcompress

import (
	"compress/gzip"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"

	"gopkg.in/kothar/brotli-go.v0/enc"
)

const (
	none   = iota
	gzType = iota
	brType = iota
)

var brotliParam = enc.NewBrotliParams()

func init() {
	brotliParam.SetQuality(3)
}

type outBuf struct {
	b                   []byte
	req                 *http.Request
	http.ResponseWriter // For APIs only.
	compressor          io.WriteCloser
	output              io.Writer
	cmpType             int
}

var bufpool = &sync.Pool{
	New: func() interface{} {
		return make([]byte, 0, 1024)
	},
}

// Dynamic Compression middleware supporting Brotoli & Gzip.
// Uses a static-compiled Brotoli library (no external dep). Have a compiler ready.
// Optimized for low CPU usage: 80kb/ms today.
// Br(2) or else Gz(3)
// Logs to a stat collector (if not nil)
func Dynamic(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		buf, closer := DynamicDIY(w, req)
		defer closer()
		handler.ServeHTTP(buf, req)
	})
}

// DynamicDIY makes it easy for wrapping for custom routers (ex: Gin)
func DynamicDIY(w http.ResponseWriter, req *http.Request) (newWriter http.ResponseWriter, complete func()) {
	buf := &outBuf{nil, req, w, nil, w, none}
	return buf, func() {
		defer func() {
			if buf.b != nil {
				bufpool.Put(buf.b[:0])
			}
		}()

		if buf.compressor != nil { // If there's data in compressor, finalize it
			buf.compressor.Close() // Ignore close errors since part-written already.
		}
		if _, err := buf.output.Write(buf.b); err != nil {
			log.Println(err)
			return
		}
	}
}

func (o *outBuf) Write(b []byte) (i int, err error) {
	if o.compressor == nil {
		if len(o.b)+len(b) < 1024 { // under 1024 bytes
			if o.b == nil {
				o.b = bufpool.Get().([]byte)
			}
			o.b = append(o.b, b...) // Copy. Never keep "b"
			return len(b), nil
		}
		o.compressor, o.cmpType, err = o.getCompressWriter(o.req, o.output)
		if err != nil {
			return 0, err
		}
		if len(o.b) != 0 {
			amnt, err := o.compressor.Write(o.b)
			if err != nil {
				return amnt, err
			}
		}
	}
	return o.compressor.Write(b)
}

func (o *outBuf) getCompressWriter(req *http.Request, output io.Writer) (input io.WriteCloser, encoding int, err error) {
	encoding = o.shouldCompress()
	input, err = makeCompressor(encoding, o.ResponseWriter)
	return input, encoding, err
}
func makeCompressor(encoding int, w http.ResponseWriter) (input io.WriteCloser, err error) {
	var cmp io.WriteCloser
	h := w.Header()
	switch encoding {
	case gzType:
		cmp, err = gzip.NewWriterLevel(w, 4)
		if err != nil {
			return nil, err
		}
		headersFor(h, encoding)
	case brType:
		cmp = enc.NewBrotliWriter(brotliParam, w)
		headersFor(h, encoding)
	default:
		cmp = &fakecloser{w} // closer may be double-called
	}
	h.Set("Vary", "Accept-Encoding") // Tells CDNs to respect Accept-Encoding
	return cmp, nil
}

var ceString = map[int]string{
	gzType: "gzip",
	brType: "br",
	none:   "identity",
}

func headersFor(h http.Header, encoding int) {
	delete(h, "content-length")
	delete(h, "Content-Length")
	ce := "Content-Encoding"
	if h.Get("Content-Range") != "" {
		ce = "TE"
	}
	h.Set(ce, ceString[encoding])
}

type fakecloser struct {
	io.Writer
}

func (f *fakecloser) Close() error { return nil }

func (o *outBuf) shouldCompress() int {
	// pprof acts badly
	p := o.req.URL.Path
	if len(p) > 11 && p[:12] == "/debug/pprof" {
		return none
	}

	// Can we prove it's already compressed?
	if alreadyCompressed(o.ResponseWriter.Header().Get("Content-Type")) {
		return none
	}

	// The browser wants...
	return browserWants(o.req)
}

func browserWants(r *http.Request) int {
	ae := r.Header.Get("Accept-Encoding")
	if strings.Contains(ae, "br") && (r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https") {
		return brType
	}
	if strings.Contains(ae, "gzip") {
		return gzType
	}
	return none
}

func alreadyCompressed(mime string) bool {
	if len(mime) >= 5 {
		ctStart := mime[:6]
		if ctStart == "image/" {
			t := mime[6:]
			if t != "gif" && t != "png" {
				return true
			}
		} else if ctStart == "video/" || ctStart == "audio/" {
			return true
		} else if len(mime) >= 9 && mime[:9] == "font/woff" {
			return true
		}
	}
	return false
}
