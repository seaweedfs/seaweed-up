package scripts

import (
	"bytes"
	"embed"
	"io"
	"io/fs"
	"text/template"
)

//go:embed *.sh
var content embed.FS

// HostPrepScript is the embedded host preparation script applied to every
// target host before deployment when --host-prep is set.
//
//go:embed host_prep.sh
var HostPrepScript string

func Open(path string) (fs.File, error) {
	return content.Open(path)
}

func RenderScript(name string, data interface{}) (io.Reader, error) {
	var buf bytes.Buffer

	t, err := template.ParseFS(content, name)
	if err != nil {
		return nil, err
	}

	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}

	return &buf, nil
}
