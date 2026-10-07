package admintitle

import (
	"io"
	"io/fs"
	"strings"
	"testing"
)

func TestAdminTitleReplaced(t *testing.T) {
	f, err := FS().Open("index.html")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	body, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Contains(text, oldTitle) || !strings.Contains(text, newTitle) {
		t.Fatalf("index title not rewritten")
	}
	js, err := findJS(FS())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(js, oldTitle) || !strings.Contains(js, newTitle) {
		t.Fatal("layout title not rewritten")
	}
}

func findJS(fsys fs.FS) (string, error) {
	var found string
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || found != "" {
			return err
		}
		if !strings.HasSuffix(name, ".js") {
			return nil
		}
		f, err := fsys.Open(name)
		if err != nil {
			return err
		}
		defer f.Close()
		body, err := io.ReadAll(f)
		if err != nil {
			return err
		}
		if strings.Contains(string(body), newTitle) {
			found = string(body)
		}
		return nil
	})
	if found == "" {
		return "", fs.ErrNotExist
	}
	return found, err
}
