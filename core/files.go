package core

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ErrConflict means a file changed on disk since the app last read it. The
// app never overwrites such a file; it reloads instead.
var ErrConflict = errors.New("the file was changed outside the app; reloaded, please try again")

type fileInfo struct {
	path string
	hash [32]byte
	doc  *yaml.Node // mapping node as read, so unknown keys survive a save
}

func readYAML(path string, v any) (fileInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return fileInfo{}, err
	}
	fi := fileInfo{path: path, hash: sha256.Sum256(data)}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fi, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fi, errors.New("expected a YAML mapping (key: value lines)")
	}
	if err := doc.Content[0].Decode(v); err != nil {
		return fi, err
	}
	fi.doc = doc.Content[0]
	return fi, nil
}

// encodeYAML renders v. Keys from the original file that v does not know
// about are kept, as are comments on known keys.
func encodeYAML(v any, orig *yaml.Node) ([]byte, *yaml.Node, error) {
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return nil, nil, err
	}
	if orig != nil {
		known := map[string]*yaml.Node{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			known[n.Content[i].Value] = n.Content[i]
		}
		for i := 0; i+1 < len(orig.Content); i += 2 {
			k := orig.Content[i]
			if nk, ok := known[k.Value]; ok {
				nk.HeadComment, nk.LineComment, nk.FootComment = k.HeadComment, k.LineComment, k.FootComment
				continue
			}
			n.Content = append(n.Content, k, orig.Content[i+1])
		}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&n); err != nil {
		return nil, nil, err
	}
	enc.Close()
	return buf.Bytes(), &n, nil
}

// writeYAML saves v over fi.path. It refuses with ErrConflict when the file on
// disk is not the version that was read (or, for a new file, already exists).
func writeYAML(fi *fileInfo, v any) error {
	data, node, err := encodeYAML(v, fi.doc)
	if err != nil {
		return err
	}
	current, err := os.ReadFile(fi.path)
	switch {
	case err == nil && sha256.Sum256(current) != fi.hash:
		return ErrConflict
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return err
	case err != nil && fi.hash != [32]byte{}:
		return ErrConflict // deleted outside the app
	}
	if err := atomicWrite(fi.path, data); err != nil {
		return err
	}
	fi.hash = sha256.Sum256(data)
	fi.doc = node
	return nil
}

// atomicWrite writes to a temporary file in the same folder and renames it
// over the target, so a crash or a sync tool never sees half a file.
func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// slugify turns a title into a short, Windows-safe file or folder name.
func slugify(s string, max int) string {
	s = nonSlug.ReplaceAllString(strings.ToLower(s), "-")
	s = strings.Trim(s, "-")
	if len(s) > max {
		s = strings.TrimRight(s[:max], "-")
	}
	return s
}

func taskFileName(id int, title string) string {
	if s := slugify(title, 40); s != "" {
		return fmt.Sprintf("%d-%s.yaml", id, s)
	}
	return fmt.Sprintf("%d.yaml", id)
}

// isDataFile reports whether name is a file the app reads: *.yaml, and not a
// hidden or temporary file or a sync tool's conflict copy.
func isDataFile(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".yaml") &&
		!strings.HasPrefix(name, ".") &&
		!strings.Contains(strings.ToLower(name), "conflict")
}
