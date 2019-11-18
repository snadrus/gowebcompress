package gincompress

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/snadrus/gowebcompress"
)

// Dynamic provides gowebcomress dynamic to gin Use().
// Set custom values in gowebcompress directly.
func Dynamic(c gin.Context) {
	buf, closer := gowebcompress.DynamicDIY(c.Writer, c.Request)
	defer closer()
	orig := c.Writer
	c.Writer = &writer{c.Writer, buf}
	c.Next()
	c.Writer = orig
}

// TODO statics. Tricky: HEAD. Accept Range, TE (transfer encoding)
// gin relies on http.FileSystem
// cannot just rewrite requested file to another name b/c ranges foul-up: they're based on the original range.
// solution 1: abandon static on range requests else yield to http.FileSystem with compression:none
// solution 2: ensure dynamic is used and set TE header. OVERRIDE??
// because we MUST NOT set encoding on outbound in dynamic wrapper nor local.
// Also, respect static inside dynamic (no change if set).

type writer struct {
	gin.ResponseWriter
	w http.ResponseWriter
}

func (w *writer) Header() http.Header {
	return w.w.Header()
}

func (w *writer) WriteHeader(statuscode int) {
	w.w.WriteHeader(statuscode)
}
func (w *writer) Write(b []byte) (int, error) {
	return w.w.Write(b)
}
