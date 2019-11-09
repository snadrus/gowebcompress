package gincompress

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/snadrus/gowebcompress"
)

// Dynamic provides gowebcomress dynamic to gin Use().
// Set custom values in gowebcompress directly.
func Dynamic(c *gin.Context) {
	buf, closer := gowebcompress.DynamicDIY(c.Writer, c.Request)
	defer closer()
	orig := c.Writer
	c.Writer = &writer{c.Writer, buf}
	c.Next()
	c.Writer = orig
}

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
