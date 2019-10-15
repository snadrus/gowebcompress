package gowebcompress

import (
	"compress/gzip"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sync"

	"gopkg.in/kothar/brotli-go.v0/enc"
)

// StaticOpts are NewStatic input for options.
type StaticOpts func(*StaticObj)

// SetDest lets you keep files in your preferred location
// as /tmp deletes them frequently.
func SetDest(dest string) StaticOpts {
	return func(staticObj *StaticObj) {
		staticObj.dest = dest
	}
}

// SetCompressionLevel lets you select something other than
// the max compression. 0 disables that type.
func SetCompressionLevel(gz, br int) StaticOpts {
	return func(staticObj *StaticObj) {
		staticObj.gz = gz
		staticObj.br = br
	}
}

// SetParallelism lets you select something other than
// the max compression. 0 disables that type.
func SetParallelism(p int) StaticOpts {
	return func(staticObj *StaticObj) {
		staticObj.numWorkers = p
	}
}

// NewStatic provides a tool for accelerating a static request.
// Opts are available to adjust its behavior.
// This call starts background workers to pre-cache the content in a non-blocking way.
// Use the member functions in handlers to do the actual send.
func NewStatic(srcFolder string, opts ...StaticOpts) *StaticObj {
	s := &StaticObj{gz: 9, br: 11, dest: "/tmp/gowebcache", src: srcFolder, numWorkers: 4}
	for _, o := range opts {
		o(s)
	}

	os.MkdirAll(s.dest, os.ModePerm|os.ModeDir)

	s.Compress(s.src)
	return s
}

// Compress enqueues files under this path to be compressed
// into the cache. Only files newer than the previous cache run will
// be processed. This is called when Static is initialized or a new
// file is served and is rarely needed otherwise.
func (s *StaticObj) Compress(path string) {
	s.walkerLock.Lock()
	defer s.walkerLock.Unlock()
	s.walkerPaths = append(s.walkerPaths, path)
	if !s.walkerIsRunning {
		go s.walker()
	}
}

// StaticObj enables high-compression static local content sends.
type StaticObj struct {
	gz         int
	br         int
	dest       string
	src        string
	numWorkers int

	walkerLock      sync.Mutex
	walkerIsRunning bool
	walkerPaths     []string
}

// absPath returns the safe paths. It needs an extension to be valid.
func (s *StaticObj) absPath(relPath string, encoding int) (src string, cache string, err error) {
	q := path.Join(s.src, relPath)
	if len(s.src) > len(q) || s.src != q[:len(s.src)] {
		return "", "", errors.New("Request attempts to escape static with: " + relPath)
	}
	if encoding == none {
		return q, q, nil
	}
	r := path.Join(s.dest, relPath)
	if len(s.dest) > len(q) || s.dest != q[:len(s.dest)] {
		return "", "", errors.New("Request attempts to escape static with: " + relPath)
	}
	return q, r + healthyCache[encoding], nil
}

var healthyCache = map[int]string{
	none:   ".gz", // Check GZ if not specified
	brType: ".br",
	gzType: ".gz",
}

// SendFile will send the browser a file. It presumes nothing else was sent
// to the writer so far. Headers can be set, but not "Content-Encoding".
// Path is typically r.URL.Path() but internal rewrites are OK.
// If the file is missing from the cache, a quick compression is sent & slow
// compression is enqueued.
func (s *StaticObj) SendFile(r *http.Request, w http.ResponseWriter, relPath string) error {
	encoding := browserWants(r)
	srcPath, cachePath, err := s.absPath(relPath, encoding)
	if err != nil {
		return err
	}
	sstat, err := os.Stat(srcPath)
	if err != nil {
		return err // err src not found
	}

	cstat, cerr := os.Stat(cachePath)
	if cerr == nil && cstat.Size() == 0 { // Handle the "SHOULD NOT COMPRESS" case
		sendFile(srcPath, w)
		return nil
	}
	if cerr != nil || sstat.ModTime().After(cstat.ModTime()) { // cache outdated
		s.Compress(cachePath)
		uncompresedWriter, err := makeCompressor(encoding, w)
		if err != nil {
			return err
		}
		err = sendFile(srcPath, uncompresedWriter)
		if err != nil {
			return err
		}
	} else { // serve cached bits
		headersFor(w.Header(), encoding)
		sendFile(cachePath, w)
	}
	return nil
}

func sendFile(path string, w io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	if err != nil {
		return err
	}
	return nil
}
func (s *StaticObj) walker() {
	for {
		s.walkerLock.Lock()
		if len(s.walkerPaths) == 0 {
			s.walkerIsRunning = false
			s.walkerLock.Unlock()
			return
		}
		rootpath := s.walkerPaths[0]
		copy(s.walkerPaths, s.walkerPaths[1:])
		s.walkerLock.Unlock()
		ch := make(chan string)
		defer close(ch)
		for g := runtime.NumCPU(); g > 0; g-- {
			go func() {
				for path := range ch {
					if s.gz != 0 {
						if err := s.makeStaticCompressed(path, gzType, s.gz); err != nil {
							log.Println(err.Error())
						}
					}
					if s.br != 0 {
						if err := s.makeStaticCompressed(path, brType, s.br); err != nil {
							log.Println(err.Error())
						}
					}
				}
			}()
		}
		err := filepath.Walk(rootpath, func(walkpath string, info os.FileInfo, err error) error {
			if err != nil {
				return filepath.SkipDir
			}
			if info.IsDir() || info.Size() < 1024 {
				return nil
			}
			if dest, err := os.Stat(path.Join(s.dest, walkpath)); err == nil && info.ModTime().Before(dest.ModTime()) {
				return nil
			}
			ch <- walkpath

			return nil
		})
		if err != nil {
			log.Println(err.Error())
		}
	}
}

func (s *StaticObj) makeStaticCompressed(srcPath string, encoding int, level int) error {
	input, err := os.Open(srcPath)
	if err != nil {
		return err
	}

	outPath := path.Join(s.dest, srcPath) + healthyCache[encoding] + "tmp"
	os.MkdirAll(path.Base(outPath), os.ModeDir|os.ModePerm)
	outFile, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer outFile.Close()
	if encoding == brType {
		cmp, err := gzip.NewWriterLevel(outFile, level)
		if err != nil {
			return err
		}
		if _, err := io.Copy(cmp, input); err != nil {
			return err
		}
	} else {
		brotliParam := enc.NewBrotliParams()

		brotliParam.SetQuality(level)
		cmp := enc.NewBrotliWriter(brotliParam, outFile)
		if _, err := io.Copy(cmp, input); err != nil {
			return err
		}
	}
	if err := outFile.Sync(); err != nil {
		return err
	}
	outstat, err := outFile.Stat()
	if err != nil {
		return err
	}
	instat, err := input.Stat()
	if err != nil {
		return err
	}
	if outstat.Size()*9/10 > instat.Size() {
		// It's too big to be worth it.
		// Flag with a zero-size file
		f, err := os.Create(outPath[:len(outPath)-3])
		if err != nil {
			return err
		}
		f.Close()
		return nil
	}
	if err := outFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(outPath, outPath[:len(outPath)-3]); err != nil {
		return err
	}
	return nil
}

// Good-to-serve: .gz .br    in-progress: .gztmp .brtmp
// if walker is done & miss occurs, restart walker.
