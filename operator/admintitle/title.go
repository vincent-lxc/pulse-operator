// 本文件把管理界面文档标题从框架默认的 BitZoom Exchange Admin 改成 Pulse Operator。
// 框架把标题写进嵌入的 HTML 和前端配置，IService 没有标题设置，所以这里包一层只读文件系统，
// 在送出页面前替换那一串字，其余静态资源原样转发。
package admintitle

import (
	"bytes"
	"embed"
	"io"
	"io/fs"
	"path"
	"strings"
	"time"

	_ "github.com/digitalwayhk/core/pkg/server/run"
	_ "unsafe"
)

const (
	oldTitle = "BitZoom Exchange Admin"
	newTitle = "Pulse Operator"
)

//go:linkname coreHTML github.com/digitalwayhk/core/pkg/server/run.html
var coreHTML embed.FS

// FS 返回替换过标题的管理前端。挂到 ServerOption.Demo 且 Pattern 为空时，
// 框架会用它代替自带的文件服务。
func FS() fs.FS {
	sub, err := fs.Sub(coreHTML, "dist")
	if err != nil {
		return rewriteFS{base: coreHTML}
	}
	return rewriteFS{base: sub}
}

type rewriteFS struct{ base fs.FS }

func (r rewriteFS) Open(name string) (fs.File, error) {
	name = strings.TrimPrefix(path.Clean("/"+name), "/")
	if name == "" || name == "." {
		return r.base.Open(".")
	}
	f, err := r.base.Open(name)
	if err != nil {
		if path.Ext(name) == "" {
			return r.Open("index.html")
		}
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return f, nil
	}
	if !rewriteName(name) {
		return f, nil
	}
	body, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil {
		return nil, err
	}
	body = bytes.ReplaceAll(body, []byte(oldTitle), []byte(newTitle))
	return &memFile{name: path.Base(name), data: body}, nil
}

func rewriteName(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	return ext == ".html" || ext == ".js" || ext == ".css"
}

type memFile struct {
	name string
	data []byte
	off  int
}

func (m *memFile) Stat() (fs.FileInfo, error) {
	return memInfo{name: m.name, size: int64(len(m.data))}, nil
}
func (m *memFile) Read(p []byte) (int, error) {
	if m.off >= len(m.data) {
		return 0, io.EOF
	}
	n := copy(p, m.data[m.off:])
	m.off += n
	return n, nil
}
func (m *memFile) Close() error { return nil }

type memInfo struct {
	name string
	size int64
}

func (m memInfo) Name() string       { return m.name }
func (m memInfo) Size() int64        { return m.size }
func (m memInfo) Mode() fs.FileMode  { return 0o644 }
func (m memInfo) ModTime() time.Time { return time.Unix(0, 0) }
func (m memInfo) IsDir() bool        { return false }
func (m memInfo) Sys() any           { return nil }
