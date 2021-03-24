package gowebcompress

import (
	"io/fs"
	"os"
	"path"
)

type OSFSStat struct {
	fs.FS
	path string
}

func NewOSFSStat(path string) *OSFSStat {
	return &OSFSStat{os.DirFS(path), path}
}

func (o *OSFSStat) Stat(p string) (fs.FileInfo, error) {
	return os.Stat(path.Join(o.path, p))
}
