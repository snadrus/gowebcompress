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
		buf := &outBuf{nil, req, w, nil, w, none}
		defer func() {
			if buf.b != nil {
				bufpool.Put(buf.b[:0])
			}
		}()
		handler.ServeHTTP(buf, req) // Do processing
		if buf.compressor != nil {  // If there's data in compressor, finalize it
			buf.compressor.Close() // Ignore close errors since part-written already.
		}
		if _, err := buf.output.Write(buf.b); err != nil {
			log.Println(err)
			return
		}
	})
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

func (o *outBuf) getCompressWriter(req *http.Request, output io.Writer) (wc io.WriteCloser, encoding int, err error) {
	var cmp io.WriteCloser
	encoding = o.shouldCompress()
	h := o.ResponseWriter.Header()
	switch encoding {
	case gzType:
		cmp, err = gzip.NewWriterLevel(output, 4)
		if err != nil {
			return nil, 0, err
		}
		rmContentLength(h)
		h.Set("Content-Encoding", "gzip")
	case brType:
		cmp = enc.NewBrotliWriter(brotliParam, output)
		rmContentLength(h)
		h.Set("Content-Encoding", "br")
	default:
		cmp = &fakecloser{output} // closer may be double-called
	}
	h.Set("Vary", "Accept-Encoding")
	return cmp, encoding, nil
}

func rmContentLength(h http.Header) {
	delete(h, "content-length")
	delete(h, "Content-Length")
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
	ae := o.req.Header.Get("Accept-Encoding")
	if strings.Contains(ae, "br") && (o.req.TLS != nil || o.req.Header.Get("X-Forwarded-Proto") == "https") {
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
