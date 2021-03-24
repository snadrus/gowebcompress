// Package gowebcompress applies top compression hueristics to
// offer a dynamic (for APIs) and Static http middleware to
// accelerate your server.
package gowebcompress

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/itchio/go-brotli/enc"
)

var DynamicLevels = Levels{2, 2}
var StaticLevels = Levels{6, 4}

const (
	none   = iota
	gzType = iota
	brType = iota
)

type Levels struct {
	Gzip   int
	Brotli int
}

type outBuf struct {
	b                   []byte
	req                 *http.Request
	http.ResponseWriter // For APIs only.
	compressor          io.WriteCloser
	output              io.Writer
	cmpType             int
	Errors              []error
}

var bufpool = &sync.Pool{
	New: func() interface{} {
		return make([]byte, 0, 1024)
	},
}

// Middleware for compression supporting Brotoli & Gzip.
// Uses a static-compiled Brotoli library (no external dep). Have a compiler ready.
// Optimized for low CPU usage: 80kb/ms today, or change DynamicLevels / StaticLevels.
func Middleware(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		buf, closer := Handler(w, req)
		defer closer()
		handler.ServeHTTP(buf, req)
	})
}

// Handler makes it easy for wrapping for custom routers (ex: Gin)
func Handler(w http.ResponseWriter, req *http.Request) (newWriter http.ResponseWriter, complete func()) {
	buf := &outBuf{nil, req, w, nil, w, none, nil}
	return buf, func() {
		defer func() {
			if buf.b != nil {
				bufpool.Put(buf.b[:0])
			}
		}()

		if buf.compressor != nil { // If there's data in compressor, finalize it
			err := buf.compressor.Close()
			if err != nil {
				buf.Errors = append(buf.Errors, err)
			}
		}
		if _, err := buf.output.Write(buf.b); err != nil {
			buf.Errors = append(buf.Errors, err)
			return
		}
		if c, ok := buf.compressor.(*fsCache); ok { // first compress, write MIME
			c.WriteMIME(buf.Header().Get("content-type"))
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
		amnt, err := o.compressorCatchup(DynamicLevels, nil)
		if err != nil {
			return amnt, err
		}
	}
	return o.compressor.Write(b)
}

func (o *outBuf) compressorCatchup(l Levels, cacher *fsCache) (int, error) {
	var err error
	o.compressor, o.cmpType, err = o.getCompressWriter(o.req, o.output, l, cacher)
	if err != nil || len(o.b) == 0 {
		return 0, err
	}
	return o.compressor.Write(o.b)
}

func (o *outBuf) getCompressWriter(req *http.Request, output io.Writer, l Levels, cacher *fsCache) (input io.WriteCloser, encoding int, err error) {
	encoding = o.shouldCompress()
	input, err = makeCompressor(encoding, o.ResponseWriter, l, cacher)
	return input, encoding, err
}

func makeCompressor(encoding int, w http.ResponseWriter, levels Levels, cacher *fsCache) (input io.WriteCloser, err error) {
	var cmp io.WriteCloser
	h := w.Header()
	var out io.Writer = w
	if cacher != nil {
		out = io.MultiWriter(cacher.disk, w)
	}
	switch encoding {
	case gzType:
		cmp, err = gzip.NewWriterLevel(out, levels.Gzip)
		if err != nil {
			return nil, err
		}
		headersFor(h, encoding)
	case brType:
		var brotliParam = &enc.BrotliWriterOptions{Quality: levels.Brotli}
		cmp = enc.NewBrotliWriter(out, brotliParam)
		headersFor(h, encoding)
	default:
		cmp = &fakecloser{out} // closer may be double-called
	}
	h.Set("Vary", "Accept-Encoding") // Tells CDNs to respect Accept-Encoding
	if cacher != nil {
		cacher.wc = cmp
		return cacher, nil
	}
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

// fsCache will cache if we have a place to cache.
type fsCache struct {
	disk     io.WriteCloser
	wc       io.WriteCloser
	mimefile io.WriteCloser
}

func (m *fsCache) Write(b []byte) (i int, err error) {
	return m.wc.Write(b)
}

func (m *fsCache) Close() error {
	m.wc.Close() // ignore gzip errors because bytes are already written.
	return m.disk.Close()
}

func (m *fsCache) WriteMIME(s string) {
	m.mimefile.Write([]byte(s))
	m.mimefile.Close()
}

func (o *outBuf) FS(sys fs.FS, origPath string, creat CreateFile, staticBase string) (handled bool) {
	if o.req.Method != http.MethodGet {
		return false // don't cache
	}

	origFullPath := path.Join(staticBase, origPath)
	if !strings.HasPrefix(origFullPath, staticBase) {
		o.Errors = append(o.Errors, errors.New("Illegal path: "+origFullPath))

		return false // don't share something outside the base.
	}

	st, ok := sys.(fs.StatFS)
	if !ok || st == nil {
		o.Errors = append(o.Errors, fmt.Errorf("isn't statfs"))
	}
	o.cmpType = o.shouldCompress()
	dest := origFullPath + "." + ceString[o.cmpType]
	if o.cmpType == none {
		return false
	}

	if compressedStat, err := st.Stat(dest); err == nil {
		origStat, err := st.Stat(origFullPath)
		if err != nil || compressedStat.IsDir() || origStat.IsDir() {
			return false // no file. No future.
		}
		if h := o.req.Header.Get("if-modified-since"); len(h) > 0 {
			if t, err := time.Parse(time.RFC1123, h); err == nil {
				if origStat.ModTime().Before(t) {
					o.ResponseWriter.WriteHeader(304)
					return true
				}
			}
		}
		// Compare timestamps orig vs disk. if newer, write & handled.
		if compressedStat.ModTime().After(origStat.ModTime()) {
			mimepath := origFullPath + ".mime"
			if dest[0] == '/' {
				dest = dest[1:]
				mimepath = mimepath[1:]
			}

			f, err := st.Open(dest)
			if err != nil {
				o.Errors = append(o.Errors, fmt.Errorf("open err: %w", err))
				return false
			}
			defer f.Close()

			fm, err := st.Open(mimepath)
			if err != nil {
				o.Errors = append(o.Errors, fmt.Errorf("open err: %w", err))
				return false
			}
			defer fm.Close()
			b, err := io.ReadAll(fm)
			if err != nil {
				o.Errors = append(o.Errors, fmt.Errorf("fm read err: %w", err))
				return false
			}
			h := o.ResponseWriter.Header()
			h.Add("content-type", string(b))
			headersFor(h, o.cmpType)

			_, err = io.Copy(o.ResponseWriter, f)
			if err != nil {
				o.Errors = append(o.Errors, fmt.Errorf("Copy err: %w", err))
				return true
			}
			return true
		}
	}

	if o.compressor == nil /* No bytes written */ {
		outfile, err := creat(dest)
		if err != nil {
			o.Errors = append(o.Errors, fmt.Errorf("can't create file: %w", err))
			return
		}
		mimefile, err := creat(origFullPath + ".mime")
		if err != nil {
			o.Errors = append(o.Errors, fmt.Errorf("can't create file: %w", err))
			return
		}

		_, err = o.compressorCatchup(StaticLevels, &fsCache{disk: outfile, mimefile: mimefile})
		if err != nil {
			o.Errors = append(o.Errors, err)
		}
	}
	return false
}

type CacheOpts struct {
	fs.FS
	CreateFile
	BasePath string
}

// FS is a convenience function for informing the cacher
// that a static file is being served and the compressed contents
// can be cached there.
// It requires the Middleware to have ran & fs must support StatFS.
// Use in handler (for static/foo.txt):
// if gowebcompress.FS(w, os.DirFS("/"), os.Create, "static/foo.txt") {
//   return;  // Bytes sent from Disk Cache
// }
// serveFile("static/foo.txt")
func FS(w io.Writer, opts CacheOpts, origPath string) (handled bool) {
	return w.(*outBuf).FS(opts.FS, origPath, opts.CreateFile, opts.BasePath)
}

type CreateFile func(path string) (io.WriteCloser, error)

type CacheInfo interface {
	GetErrors() []error
}

func (o *outBuf) GetErrors() []error {
	return o.Errors
}

func OSCreate(abspath string) (io.WriteCloser, error) {
	return os.Create(abspath)
}
