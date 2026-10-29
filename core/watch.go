package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watch watches the library for edits made outside the app and calls
// onChange after the index has been reloaded. Bursts of events (a sync tool
// writing many files) are collapsed into one reload. It returns when ctx is
// cancelled.
func (s *Store) Watch(ctx context.Context, onChange func()) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()
	watched := map[string]bool{}
	addDirs := func() {
		dirs := []string{s.root}
		entries, _ := os.ReadDir(s.root)
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				dirs = append(dirs, filepath.Join(s.root, e.Name()), filepath.Join(s.root, e.Name(), "tasks"))
			}
		}
		for _, d := range dirs {
			if !watched[d] && w.Add(d) == nil {
				watched[d] = true
			}
		}
	}
	addDirs()

	timer := time.NewTimer(time.Hour)
	timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
				delete(watched, ev.Name)
			}
			timer.Reset(250 * time.Millisecond)
		case <-w.Errors:
			// Usually an event overflow: a full rescan covers it.
			timer.Reset(250 * time.Millisecond)
		case <-timer.C:
			addDirs()
			if s.Rescan() {
				onChange()
			}
		}
	}
}
