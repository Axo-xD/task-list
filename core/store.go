package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Problem is a file the app could not use as-is. A file that cannot be read
// at all is never written by the app. A task with a value that cannot be
// used (see kept) is shown with a stand-in and keeps that value on disk.
type Problem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type projectState struct {
	p     *Project
	tasks map[int]*Task
	// trashedID is the highest task id among this project's deleted tasks,
	// so a new task never takes the id of one that can still be restored.
	trashedID int
}

// Store is the in-memory index of one library folder.
type Store struct {
	mu   sync.Mutex
	root string
	now  func() time.Time
	index
}

// index is everything read from the library in one pass.
type index struct {
	projects map[string]*projectState
	inbox    *Inbox // nil when inbox.yaml exists but cannot be read
	problems []Problem
	seen     map[string][32]byte // every file read, for change detection
}

// Open reads the library folder, which must exist: a missing folder (say, an
// unplugged drive) is reported rather than silently created.
func Open(root string) (*Store, error) {
	return openWithClock(root, time.Now)
}

func openWithClock(root string, now func() time.Time) (*Store, error) {
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("the library folder %s was not found", root)
	}
	return &Store{root: root, now: now, index: load(root)}, nil
}

func (s *Store) Root() string { return s.root }

func (s *Store) Problems() []Problem {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Problem(nil), s.problems...)
}

// Rescan rereads the library and reports whether anything changed since the
// last read or write by the app. The watcher calls it after file events.
func (s *Store) Rescan() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := load(s.root)
	changed := len(idx.seen) != len(s.seen)
	if !changed {
		for path, h := range idx.seen {
			if s.seen[path] != h {
				changed = true
				break
			}
		}
	}
	if changed {
		s.index = idx
	}
	return changed
}

func (s *Store) reloadLocked() {
	s.index = load(s.root)
}

func load(root string) index {
	projects := map[string]*projectState{}
	var problems []Problem
	seen := map[string][32]byte{}
	problem := func(path string, format string, args ...any) {
		problems = append(problems, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}

	dirs, err := os.ReadDir(root)
	if err != nil {
		problem(root, "cannot read library folder: %v", err)
		return index{projects: projects, problems: problems, seen: seen}
	}
	inbox := &Inbox{file: fileInfo{path: filepath.Join(root, inboxFile)}}
	if _, err := os.Stat(inbox.file.path); err == nil {
		fi, err := readYAML(inbox.file.path, inbox)
		seen[inbox.file.path] = fi.hash
		inbox.file = fi
		if err != nil {
			problem(fi.path, "%v", err)
			inbox = nil
		}
	}
	for _, d := range dirs {
		if !d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			continue
		}
		dir := filepath.Join(root, d.Name())
		path := filepath.Join(dir, "project.yaml")
		if _, err := os.Stat(path); err != nil {
			continue // not a project folder
		}
		p := &Project{}
		fi, err := readYAML(path, p)
		seen[path] = fi.hash
		if err != nil {
			problem(path, "%v", err)
			continue
		}
		p.Slug, p.file = d.Name(), fi
		if p.Name == "" {
			p.Name = d.Name()
		}
		if len(p.Columns) == 0 {
			problem(path, "no columns defined; using the default columns")
			p.Columns = DefaultColumns()
		}
		ps := &projectState{p: p, tasks: map[int]*Task{}, trashedID: trashedID(filepath.Join(root, trashDir, p.Slug))}
		projects[p.Slug] = ps

		taskDir := filepath.Join(dir, "tasks")
		entries, _ := os.ReadDir(taskDir)
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(name), ".yaml") || strings.HasPrefix(name, ".") {
				continue
			}
			tpath := filepath.Join(taskDir, name)
			if !isDataFile(name) {
				problem(tpath, "looks like a sync conflict copy; merge it by hand and delete it")
				continue
			}
			t := &Task{}
			fi, err := readYAML(tpath, t)
			seen[tpath] = fi.hash
			if err != nil {
				problem(tpath, "%v", err)
				continue
			}
			t.file = fi
			if t.ID <= 0 {
				problem(tpath, "missing or invalid id")
				continue
			}
			if other, dup := ps.tasks[t.ID]; dup {
				problem(tpath, "id %d is already used by %s", t.ID, filepath.Base(other.file.path))
				continue
			}
			if _, ok := p.column(t.Status); !ok {
				if t.Status != "" {
					problem(tpath, "unknown status %q; shown in %q", t.Status, p.Columns[0].Name)
					t.keep.status = ptr(t.Status)
				}
				t.Status = p.Columns[0].ID
			}
			if t.Priority == "" {
				t.Priority = Medium
			} else if !t.Priority.valid() {
				problem(tpath, "unknown priority %q; treated as medium", t.Priority)
				t.keep.priority = ptr(t.Priority)
				t.Priority = Medium
			}
			if t.Milestone != nil {
				if _, ok := p.milestone(*t.Milestone); !ok {
					problem(tpath, "milestone %d not found; shown without a milestone", *t.Milestone)
					t.keep.milestone, t.Milestone = t.Milestone, nil
				}
			}
			ps.tasks[t.ID] = t
		}
		fixParents(ps, func(t *Task, msg string) { problem(t.file.path, "%s", msg) })
	}
	sort.Slice(problems, func(i, j int) bool { return problems[i].Path < problems[j].Path })
	return index{projects: projects, inbox: inbox, problems: problems, seen: seen}
}

// fixParents detaches tasks whose parent is missing or that sit in a parent
// loop, so the rest of the app can rely on a proper tree.
func fixParents(ps *projectState, problem func(*Task, string)) {
	for _, t := range ps.tasks {
		if t.Parent != nil {
			if _, ok := ps.tasks[*t.Parent]; !ok || *t.Parent == t.ID {
				problem(t, fmt.Sprintf("parent %d not found; shown as a top-level task", *t.Parent))
				t.keep.parent, t.Parent = t.Parent, nil
			}
		}
	}
	for _, t := range ps.tasks {
		visited := map[int]bool{t.ID: true}
		for cur := t; cur.Parent != nil; cur = ps.tasks[*cur.Parent] {
			if visited[*cur.Parent] {
				problem(cur, "parent loop; shown as a top-level task")
				cur.keep.parent, cur.Parent = cur.Parent, nil
				break
			}
			visited[*cur.Parent] = true
		}
	}
}

const trashDir = ".trash"

// trashName matches a file in the trash: the time it was deleted, then the
// task's own file name, which starts with its id.
var trashName = regexp.MustCompile(`^\d{8}-\d{6}-(\d+)[-.]`)

// trashedID returns the highest task id in a project's trash folder. The id
// is taken from the file name; a file renamed by hand is opened instead.
func trashedID(dir string) int {
	highest := 0
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || !isDataFile(e.Name()) {
			continue
		}
		id := 0
		if m := trashName.FindStringSubmatch(e.Name()); m != nil {
			id, _ = strconv.Atoi(m[1])
		} else {
			var t struct {
				ID int `yaml:"id"`
			}
			if _, err := readYAML(filepath.Join(dir, e.Name()), &t); err == nil {
				id = t.ID
			}
		}
		highest = max(highest, id)
	}
	return highest
}

// nextTaskID is one above every id in use, in the project or in its trash.
func (ps *projectState) nextTaskID() int {
	id := ps.trashedID
	for used := range ps.tasks {
		id = max(id, used)
	}
	return id + 1
}

// nextMilestoneID is one above every milestone id in use, counting ids that
// tasks still point to after their milestone has gone.
func (ps *projectState) nextMilestoneID() int {
	id := 0
	for _, m := range ps.p.Milestones {
		id = max(id, m.ID)
	}
	for _, t := range ps.tasks {
		if m := t.onDisk().Milestone; m != nil {
			id = max(id, *m)
		}
	}
	return id + 1
}

func ptr[T any](v T) *T { return &v }

func (s *Store) project(slug string) (*projectState, error) {
	ps, ok := s.projects[slug]
	if !ok {
		return nil, fmt.Errorf("project %q not found", slug)
	}
	return ps, nil
}

func (s *Store) task(slug string, id int) (*projectState, *Task, error) {
	ps, err := s.project(slug)
	if err != nil {
		return nil, nil, err
	}
	t, ok := ps.tasks[id]
	if !ok {
		return nil, nil, fmt.Errorf("task %d not found in %q", id, slug)
	}
	return ps, t, nil
}

// children returns the direct subtasks of parent (0 = top level), in order.
func (ps *projectState) children(parent int) []*Task {
	var out []*Task
	for _, t := range ps.tasks {
		if t.parentID() == parent {
			out = append(out, t)
		}
	}
	sortTasks(out)
	return out
}

func (ps *projectState) descendants(id int) []*Task {
	var out []*Task
	for _, c := range ps.children(id) {
		out = append(out, c)
		out = append(out, ps.descendants(c.ID)...)
	}
	return out
}

// siblings are the tasks t is ordered among: top-level tasks in the same
// column, or subtasks of the same parent.
func (ps *projectState) siblings(parent int, status string) []*Task {
	var out []*Task
	for _, t := range ps.children(parent) {
		if parent != 0 || t.Status == status {
			out = append(out, t)
		}
	}
	return out
}

func (ps *projectState) isDone(t *Task) bool {
	c, _ := ps.p.column(t.Status)
	return c.Type == Done
}

func sortTasks(ts []*Task) {
	sort.Slice(ts, func(i, j int) bool {
		if ts[i].Position != ts[j].Position {
			return ts[i].Position < ts[j].Position
		}
		return ts[i].ID < ts[j].ID
	})
}
