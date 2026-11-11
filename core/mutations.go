package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const positionStep = 1000

type ProjectInput struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
	Deadline    string `json:"deadline"` // YYYY-MM-DD or ""
	// Columns for the new board; empty means DefaultColumns.
	Columns []Column `json:"columns"`
}

// ProjectPatch changes only the fields that are set. Deadline "" clears it.
type ProjectPatch struct {
	Name        *string `json:"name"`
	Color       *string `json:"color"`
	Description *string `json:"description"`
	Deadline    *string `json:"deadline"`
	Archived    *bool   `json:"archived"`
}

type TaskInput struct {
	Title       string   `json:"title"`
	Parent      int      `json:"parent"` // 0 = top level
	Status      string   `json:"status"` // "" = first not-started column
	Priority    Priority `json:"priority"`
	Deadline    string   `json:"deadline"`
	Description string   `json:"description"`
}

// TaskPatch changes only the fields that are set. Deadline "" clears it.
type TaskPatch struct {
	Title       *string   `json:"title"`
	Description *string   `json:"description"`
	Priority    *Priority `json:"priority"`
	Deadline    *string   `json:"deadline"`
	WaitingFor  *string   `json:"waitingFor"`
	Status      *string   `json:"status"`
	Milestone   *int      `json:"milestone"` // 0 unlinks
	// CompleteSubtasks confirms moving a task to a done column while it
	// still has open subtasks; they are completed too.
	CompleteSubtasks bool `json:"completeSubtasks"`
}

// StatusResult is returned by calls that may move a task to a done column.
// When NeedsConfirm is set nothing was changed: the task has OpenSubtasks
// open subtasks, and the call must be repeated with CompleteSubtasks.
type StatusResult struct {
	NeedsConfirm bool `json:"needsConfirm"`
	OpenSubtasks int  `json:"openSubtasks"`
}

func (s *Store) CreateProject(in ProjectInput) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return "", errors.New("a project needs a name")
	}
	p := &Project{Name: name, Color: in.Color, Description: in.Description, Created: StampOf(s.now()), Columns: in.Columns}
	if len(p.Columns) == 0 {
		p.Columns = DefaultColumns()
	} else if err := ValidateColumns(p.Columns); err != nil {
		return "", err
	}
	if p.Color == "" {
		p.Color = "#2F5BEA"
	}
	if err := setDate(&p.Deadline, in.Deadline); err != nil {
		return "", err
	}
	base := slugify(name, 40)
	if base == "" {
		base = "project"
	}
	slug := base
	for i := 2; ; i++ {
		if _, err := os.Stat(filepath.Join(s.root, slug)); errors.Is(err, os.ErrNotExist) {
			break
		}
		slug = fmt.Sprintf("%s-%d", base, i)
	}
	p.Slug = slug
	p.file.path = filepath.Join(s.root, slug, "project.yaml")
	if err := os.MkdirAll(filepath.Join(s.root, slug, "tasks"), 0o755); err != nil {
		return "", err
	}
	if err := s.saveProject(p); err != nil {
		return "", err
	}
	s.projects[slug] = &projectState{p: p, tasks: map[int]*Task{}}
	return slug, nil
}

func (s *Store) UpdateProject(slug string, in ProjectPatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, err := s.project(slug)
	if err != nil {
		return err
	}
	p := *ps.p
	if in.Name != nil {
		if strings.TrimSpace(*in.Name) == "" {
			return errors.New("a project needs a name")
		}
		p.Name = strings.TrimSpace(*in.Name)
	}
	if in.Color != nil {
		p.Color = *in.Color
	}
	if in.Description != nil {
		p.Description = *in.Description
	}
	if in.Deadline != nil {
		if err := setDate(&p.Deadline, *in.Deadline); err != nil {
			return err
		}
	}
	if in.Archived != nil {
		p.Archived = *in.Archived
	}
	if err := s.saveProject(&p); err != nil {
		return err
	}
	*ps.p = p
	return nil
}

func (s *Store) CreateTask(slug string, in TaskInput) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, err := s.project(slug)
	if err != nil {
		return 0, err
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return 0, errors.New("a task needs a title")
	}
	now := StampOf(s.now())
	t := &Task{Title: title, Priority: in.Priority, Description: in.Description, Created: now, Updated: now}
	if t.Priority == "" {
		t.Priority = Medium
	} else if !t.Priority.valid() {
		return 0, fmt.Errorf("unknown priority %q", t.Priority)
	}
	if err := setDate(&t.Deadline, in.Deadline); err != nil {
		return 0, err
	}
	if in.Parent != 0 {
		if _, ok := ps.tasks[in.Parent]; !ok {
			return 0, fmt.Errorf("parent task %d not found", in.Parent)
		}
		parent := in.Parent
		t.Parent = &parent
	}
	t.Status = in.Status
	if t.Status == "" {
		t.Status = ps.p.firstOfType(NotStarted)
		if t.Status == "" {
			t.Status = ps.p.Columns[0].ID
		}
	}
	col, ok := ps.p.column(t.Status)
	if !ok {
		return 0, fmt.Errorf("unknown status %q", t.Status)
	}
	if col.Type == Done {
		t.Completed = &now
	}
	t.ID = ps.nextTaskID()
	t.Position = endPosition(ps.siblings(t.parentID(), t.Status))
	t.file.path = filepath.Join(s.root, slug, "tasks", taskFileName(t.ID, title))
	if err := s.saveTask(t); err != nil {
		return 0, err
	}
	ps.tasks[t.ID] = t
	return t.ID, nil
}

func (s *Store) UpdateTask(slug string, id int, in TaskPatch) (StatusResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, cur, err := s.task(slug, id)
	if err != nil {
		return StatusResult{}, err
	}
	t := *cur
	if in.Title != nil {
		if strings.TrimSpace(*in.Title) == "" {
			return StatusResult{}, errors.New("a task needs a title")
		}
		t.Title = strings.TrimSpace(*in.Title)
	}
	if in.Description != nil {
		t.Description = *in.Description
	}
	if in.Priority != nil {
		if !in.Priority.valid() {
			return StatusResult{}, fmt.Errorf("unknown priority %q", *in.Priority)
		}
		t.Priority, t.keep.priority = *in.Priority, nil
	}
	if in.Deadline != nil {
		if err := setDate(&t.Deadline, *in.Deadline); err != nil {
			return StatusResult{}, err
		}
	}
	if in.WaitingFor != nil {
		t.WaitingFor = optText(strings.TrimSpace(*in.WaitingFor))
	}
	if in.Milestone != nil {
		t.keep.milestone = nil
		if *in.Milestone == 0 {
			t.Milestone = nil
		} else if _, ok := ps.p.milestone(*in.Milestone); ok {
			m := *in.Milestone
			t.Milestone = &m
		} else {
			return StatusResult{}, fmt.Errorf("milestone %d not found", *in.Milestone)
		}
	}
	if in.Status != nil && *in.Status != t.Status {
		res, err := s.changeStatus(ps, &t, *in.Status, in.CompleteSubtasks)
		if err != nil || res.NeedsConfirm {
			return res, err
		}
		if t.Parent == nil {
			t.Position = arrivalPosition(ps, &t)
		}
	}
	return StatusResult{}, s.commitTask(ps, cur, &t)
}

// SetDone ticks or unticks a task: it moves to the project's first done
// column, or back to its first not-started column.
func (s *Store) SetDone(slug string, id int, done, completeSubtasks bool) (StatusResult, error) {
	s.mu.Lock()
	ps, t, err := s.task(slug, id)
	s.mu.Unlock()
	if err != nil {
		return StatusResult{}, err
	}
	status := ps.p.firstOfType(NotStarted)
	if done {
		status = ps.p.firstOfType(Done)
	}
	if status == "" {
		return StatusResult{}, errors.New("the project has no column for that")
	}
	return s.UpdateTask(slug, t.ID, TaskPatch{Status: &status, CompleteSubtasks: completeSubtasks})
}

// MoveTask moves a task to a column (top-level tasks) and places it before
// the task beforeID, or at the end when beforeID is 0.
func (s *Store) MoveTask(slug string, id int, status string, beforeID int, completeSubtasks bool) (StatusResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, cur, err := s.task(slug, id)
	if err != nil {
		return StatusResult{}, err
	}
	t := *cur
	if status != t.Status {
		res, err := s.changeStatus(ps, &t, status, completeSubtasks)
		if err != nil || res.NeedsConfirm {
			return res, err
		}
	}
	var sibs []*Task
	for _, x := range ps.siblings(t.parentID(), t.Status) {
		if x.ID != t.ID {
			sibs = append(sibs, x)
		}
	}
	at := len(sibs)
	for i, x := range sibs {
		if x.ID == beforeID {
			at = i
		}
	}
	switch {
	case len(sibs) == 0:
		t.Position = positionStep
	case at == 0:
		t.Position = sibs[0].Position - positionStep
	case at == len(sibs):
		t.Position = sibs[at-1].Position + positionStep
	case sibs[at].Position-sibs[at-1].Position >= 2:
		t.Position = (sibs[at-1].Position + sibs[at].Position) / 2
	default:
		// No room left between the neighbours: renumber the whole list.
		ordered := append(append(append([]*Task{}, sibs[:at]...), &t), sibs[at:]...)
		for i, x := range ordered {
			pos := (i + 1) * positionStep
			if x == &t {
				t.Position = pos
				continue
			}
			if x.Position != pos {
				moved := *x
				moved.Position = pos
				if err := s.writeTask(ps, x, &moved, false); err != nil {
					return StatusResult{}, err
				}
			}
		}
	}
	return StatusResult{}, s.commitTask(ps, cur, &t)
}

// changeStatus sets t's status. Moving into a done column with open
// subtasks needs confirmation; once confirmed, those subtasks are completed
// and saved immediately.
func (s *Store) changeStatus(ps *projectState, t *Task, status string, completeSubtasks bool) (StatusResult, error) {
	col, ok := ps.p.column(status)
	if !ok {
		return StatusResult{}, fmt.Errorf("unknown status %q", status)
	}
	wasDone := ps.isDone(t)
	t.Status, t.keep.status = status, nil
	if col.Type != Done {
		t.Completed = nil
		return StatusResult{}, nil
	}
	if wasDone {
		return StatusResult{}, nil
	}
	var open []*Task
	for _, d := range ps.descendants(t.ID) {
		if !ps.isDone(d) {
			open = append(open, d)
		}
	}
	if len(open) > 0 && !completeSubtasks {
		return StatusResult{NeedsConfirm: true, OpenSubtasks: len(open)}, nil
	}
	now := StampOf(s.now())
	t.Completed = &now
	for _, d := range open {
		done := *d
		done.Status, done.Completed, done.keep.status = status, &now, nil
		if err := s.writeTask(ps, d, &done, true); err != nil {
			return StatusResult{}, err
		}
	}
	return StatusResult{}, nil
}

// DeleteTask moves the task and all its subtasks to <library>/.trash.
func (s *Store) DeleteTask(slug string, id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, t, err := s.task(slug, id)
	if err != nil {
		return err
	}
	trash := filepath.Join(s.root, trashDir, slug)
	if err := os.MkdirAll(trash, 0o755); err != nil {
		return err
	}
	stamp := s.now().Format("20060102-150405")
	for _, x := range append([]*Task{t}, ps.descendants(id)...) {
		dest := filepath.Join(trash, stamp+"-"+filepath.Base(x.file.path))
		if err := os.Rename(x.file.path, dest); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.reloadLocked()
			return err
		}
		ps.trashedID = max(ps.trashedID, x.ID)
		delete(ps.tasks, x.ID)
		delete(s.seen, x.file.path)
	}
	return nil
}

// AddNote adds a note to a project (taskID 0) or to a task.
func (s *Store) AddNote(slug string, taskID int, text string) error {
	return s.editNotes(slug, taskID, func(notes []Note, now Stamp) ([]Note, error) {
		text = strings.TrimSpace(text)
		if text == "" {
			return nil, errors.New("a note can't be empty")
		}
		id := 0
		for _, n := range notes {
			id = max(id, n.ID)
		}
		return append([]Note{{ID: id + 1, Created: now, Text: text}}, notes...), nil
	})
}

func (s *Store) EditNote(slug string, taskID, noteID int, text string) error {
	return s.editNotes(slug, taskID, func(notes []Note, now Stamp) ([]Note, error) {
		text = strings.TrimSpace(text)
		if text == "" {
			return nil, errors.New("a note can't be empty")
		}
		out := append([]Note(nil), notes...)
		for i := range out {
			if out[i].ID == noteID {
				if out[i].Text != text {
					out[i].Text, out[i].Edited = text, &now
				}
				return out, nil
			}
		}
		return nil, fmt.Errorf("note %d not found", noteID)
	})
}

func (s *Store) editNotes(slug string, taskID int, edit func([]Note, Stamp) ([]Note, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := StampOf(s.now())
	if taskID == 0 {
		ps, err := s.project(slug)
		if err != nil {
			return err
		}
		p := *ps.p
		if p.Notes, err = edit(p.Notes, now); err != nil {
			return err
		}
		if err := s.saveProject(&p); err != nil {
			return err
		}
		*ps.p = p
		return nil
	}
	ps, cur, err := s.task(slug, taskID)
	if err != nil {
		return err
	}
	t := *cur
	if t.Notes, err = edit(t.Notes, now); err != nil {
		return err
	}
	return s.commitTask(ps, cur, &t)
}

// commitTask saves the edited copy t of cur and, on success, makes it current.
func (s *Store) commitTask(ps *projectState, cur, t *Task) error {
	return s.writeTask(ps, cur, t, true)
}

func (s *Store) writeTask(ps *projectState, cur, t *Task, touch bool) error {
	if touch {
		t.Updated = StampOf(s.now())
	}
	if err := s.saveTask(t); err != nil {
		return err
	}
	*cur = *t
	ps.tasks[cur.ID] = cur
	return nil
}

func (s *Store) saveTask(t *Task) error {
	if err := writeYAML(&t.file, t.onDisk()); err != nil {
		s.reloadLocked()
		return err
	}
	s.seen[t.file.path] = t.file.hash
	return nil
}

func (s *Store) saveProject(p *Project) error {
	if err := writeYAML(&p.file, p); err != nil {
		s.reloadLocked()
		return err
	}
	s.seen[p.file.path] = p.file.hash
	return nil
}

func endPosition(sibs []*Task) int {
	if len(sibs) == 0 {
		return positionStep
	}
	return sibs[len(sibs)-1].Position + positionStep
}

// arrivalPosition places a task that changed column: at the top of a done
// column (most recent first), at the end of any other.
func arrivalPosition(ps *projectState, t *Task) int {
	var sibs []*Task
	for _, x := range ps.siblings(0, t.Status) {
		if x.ID != t.ID {
			sibs = append(sibs, x)
		}
	}
	if ps.isDone(t) && len(sibs) > 0 {
		return sibs[0].Position - positionStep
	}
	return endPosition(sibs)
}

func setDate(dst **Date, s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		*dst = nil
		return nil
	}
	d, err := ParseDate(s)
	if err != nil {
		return err
	}
	*dst = &d
	return nil
}
