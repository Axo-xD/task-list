package core

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
// about are kept, and so are comments, at every level: in the file itself
// and inside its lists (notes, columns, milestones, quick list items).
func encodeYAML(v any, orig *yaml.Node) ([]byte, *yaml.Node, error) {
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return nil, nil, err
	}
	if orig != nil {
		mergeNode(&n, orig, reflect.TypeOf(v))
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

// mergeNode carries over from orig (the file as read) what the freshly
// encoded node n would otherwise lose: comments, and keys that the Go type t
// does not know. List items are matched by their id, so an item that was
// removed takes its extras with it.
func mergeNode(n, orig *yaml.Node, t reflect.Type) {
	copyComments(n, orig)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case n.Kind == yaml.MappingNode && orig.Kind == yaml.MappingNode && t != nil && t.Kind() == reflect.Struct:
		fields := ownedKeys(t)
		written := map[string]int{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			written[n.Content[i].Value] = i
		}
		for i := 0; i+1 < len(orig.Content); i += 2 {
			k, val := orig.Content[i], orig.Content[i+1]
			if j, ok := written[k.Value]; ok {
				copyComments(n.Content[j], k)
				mergeNode(n.Content[j+1], val, fields[k.Value])
				continue
			}
			if _, owned := fields[k.Value]; owned {
				continue // ours, and now empty (omitempty): drop it
			}
			n.Content = append(n.Content, k, val)
		}
	case n.Kind == yaml.SequenceNode && orig.Kind == yaml.SequenceNode && t != nil && t.Kind() == reflect.Slice:
		byID := map[string]*yaml.Node{}
		for _, item := range orig.Content {
			if id, ok := nodeID(item); ok {
				byID[id] = item
			}
		}
		for _, item := range n.Content {
			if id, ok := nodeID(item); ok && byID[id] != nil {
				mergeNode(item, byID[id], t.Elem())
			}
		}
	}
}

func copyComments(n, orig *yaml.Node) {
	n.HeadComment, n.LineComment, n.FootComment = orig.HeadComment, orig.LineComment, orig.FootComment
}

// nodeID is the value of the id key of a list item.
func nodeID(n *yaml.Node) (string, bool) {
	if n.Kind != yaml.MappingNode {
		return "", false
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == "id" {
			return n.Content[i+1].Value, true
		}
	}
	return "", false
}

// ownedKeys lists every YAML key the struct type t can write, including keys
// left out because they are empty, with the type of each.
func ownedKeys(t reflect.Type) map[string]reflect.Type {
	keys := map[string]reflect.Type{}
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
		if name != "" && name != "-" {
			keys[name] = t.Field(i).Type
		}
	}
	return keys
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

// latin spells common accented letters in plain a-z, so "Tromsø" becomes
// "tromso" rather than "troms".
var latin = strings.NewReplacer("æ", "ae", "ø", "o", "å", "a", "ä", "a", "ö", "o", "ü", "u",
	"é", "e", "è", "e", "ê", "e", "á", "a", "à", "a", "í", "i", "ó", "o", "ú", "u", "ñ", "n", "ç", "c", "ß", "ss")

// slugify turns a title into a short, Windows-safe file or folder name.
func slugify(s string, max int) string {
	s = nonSlug.ReplaceAllString(latin.Replace(strings.ToLower(s)), "-")
	s = strings.Trim(s, "-")
	if len(s) > max {
		s = strings.TrimRight(s[:max], "-")
	}
	return s
}

// Slug turns a name into a short id made of a-z, 0-9 and dashes.
func Slug(name string) string { return slugify(name, 40) }

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
