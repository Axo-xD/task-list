package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Wednesday 28 October 2026, 14:00.
var testNow = time.Date(2026, 10, 28, 14, 0, 0, 0, time.Local)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := openWithClock(t.TempDir(), func() time.Time { return testNow })
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func must[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func ptr[T any](v T) *T { return &v }

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (s *Store) taskPath(slug string, id int) string { return s.projects[slug].tasks[id].file.path }

func TestTaskFileFormatAndRoundTrip(t *testing.T) {
	s := newStore(t)
	slug := must[string](t)(s.CreateProject(ProjectInput{Name: "Website redesign", Deadline: "2026-11-12"}))
	if slug != "website-redesign" {
		t.Fatalf("slug = %q", slug)
	}
	id := must[int](t)(s.CreateTask(slug, TaskInput{Title: "Review homepage copy", Priority: High, Deadline: "2026-10-29",
		Description: "Go through all the copy.\nFlag anything for the client."}))
	if err := s.AddNote(slug, id, "First pass done."); err != nil {
		t.Fatal(err)
	}
	path := s.taskPath(slug, id)
	if filepath.Base(path) != "1-review-homepage-copy.yaml" {
		t.Errorf("file name = %s", filepath.Base(path))
	}
	got := readFile(t, path)
	for _, want := range []string{
		"id: 1\n", "title: Review homepage copy\n", "status: todo\n", "priority: high\n",
		"deadline: 2026-10-29\n", "waiting_for: null\n", "parent: null\n", "position: 1000\n", "created: 2026-10-28T14:00\n",
		"description: |-\n  Go through all the copy.\n  Flag anything for the client.\n",
		"notes:\n  - id: 1\n", "    text: First pass done.\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("file is missing %q:\n%s", want, got)
		}
	}

	// A hand edit adds a key the app does not know and a comment; both survive a save.
	edited := strings.Replace(got, "title:", "# my comment\ntitle:", 1) + "estimate: 3h\n"
	os.WriteFile(path, []byte(edited), 0o644)
	if !s.Rescan() {
		t.Fatal("Rescan did not notice the hand edit")
	}
	must[StatusResult](t)(s.UpdateTask(slug, id, TaskPatch{Title: ptr("Review all copy")}))
	got = readFile(t, path)
	for _, want := range []string{"# my comment\ntitle: Review all copy\n", "estimate: 3h\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("file is missing %q:\n%s", want, got)
		}
	}

	// Reopening the library gives the same data.
	s2 := must[*Store](t)(openWithClock(s.root, s.now))
	d := must[TaskDetail](t)(s2.Task(slug, id))
	if d.Task.Title != "Review all copy" || d.Task.Description != "Go through all the copy.\nFlag anything for the client." ||
		d.Task.Deadline.String() != "2026-10-29" || len(d.Notes) != 1 {
		t.Errorf("reloaded task = %+v", d)
	}
	if len(s2.Problems()) != 0 {
		t.Errorf("problems: %v", s2.Problems())
	}
}

func TestOwnWritesAreNotExternalChanges(t *testing.T) {
	s := newStore(t)
	slug := must[string](t)(s.CreateProject(ProjectInput{Name: "P"}))
	id := must[int](t)(s.CreateTask(slug, TaskInput{Title: "A"}))
	s.AddNote(slug, 0, "project note")
	must[StatusResult](t)(s.MoveTask(slug, id, "in-progress", 0, false))
	if s.Rescan() {
		t.Error("Rescan reported the app's own writes as a change")
	}
}

func TestConflictingExternalEditIsNotOverwritten(t *testing.T) {
	s := newStore(t)
	slug := must[string](t)(s.CreateProject(ProjectInput{Name: "P"}))
	id := must[int](t)(s.CreateTask(slug, TaskInput{Title: "A"}))
	path := s.taskPath(slug, id)
	external := strings.Replace(readFile(t, path), "title: A", "title: Edited elsewhere", 1)
	os.WriteFile(path, []byte(external), 0o644)

	// The watcher has not run yet, so the app still holds the old version.
	_, err := s.UpdateTask(slug, id, TaskPatch{Title: ptr("From the app")})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if readFile(t, path) != external {
		t.Error("the external edit was overwritten")
	}
	if d := must[TaskDetail](t)(s.Task(slug, id)); d.Task.Title != "Edited elsewhere" {
		t.Errorf("index not reloaded, title = %q", d.Task.Title)
	}
}

func TestDoneWithOpenSubtasksNeedsConfirmation(t *testing.T) {
	s := newStore(t)
	slug := must[string](t)(s.CreateProject(ProjectInput{Name: "P"}))
	parent := must[int](t)(s.CreateTask(slug, TaskInput{Title: "Parent"}))
	a := must[int](t)(s.CreateTask(slug, TaskInput{Title: "A", Parent: parent}))
	b := must[int](t)(s.CreateTask(slug, TaskInput{Title: "B", Parent: parent}))
	nested := must[int](t)(s.CreateTask(slug, TaskInput{Title: "B.1", Parent: b}))
	must[StatusResult](t)(s.SetDone(slug, a, true, false))

	res := must[StatusResult](t)(s.MoveTask(slug, parent, "done", 0, false))
	if !res.NeedsConfirm || res.OpenSubtasks != 2 {
		t.Fatalf("res = %+v, want confirmation for 2 open subtasks", res)
	}
	if d := must[TaskDetail](t)(s.Task(slug, parent)); d.Task.Done {
		t.Fatal("parent moved without confirmation")
	}

	must[StatusResult](t)(s.MoveTask(slug, parent, "done", 0, true))
	for _, id := range []int{parent, b, nested} {
		d := must[TaskDetail](t)(s.Task(slug, id))
		if !d.Task.Done || d.Task.Completed == nil {
			t.Errorf("task %d: done=%v completed=%v", id, d.Task.Done, d.Task.Completed)
		}
	}

	// Moving it out of Done clears the completion date.
	must[StatusResult](t)(s.UpdateTask(slug, parent, TaskPatch{Status: ptr("testing")}))
	if d := must[TaskDetail](t)(s.Task(slug, parent)); d.Task.Done || d.Task.Completed != nil {
		t.Errorf("after reopening: %+v", d.Task)
	}
}

func TestMoveAndReorder(t *testing.T) {
	s := newStore(t)
	slug := must[string](t)(s.CreateProject(ProjectInput{Name: "P"}))
	ids := map[string]int{}
	for _, title := range []string{"A", "B", "C"} {
		ids[title] = must[int](t)(s.CreateTask(slug, TaskInput{Title: title}))
	}
	order := func(status string) string {
		b := must[Board](t)(s.Board(slug))
		var out []string
		for _, c := range b.Columns {
			if c.ID == status {
				for _, card := range c.Cards {
					out = append(out, card.Title)
				}
			}
		}
		return strings.Join(out, "")
	}
	must[StatusResult](t)(s.MoveTask(slug, ids["C"], "todo", ids["A"], false))
	if got := order("todo"); got != "CAB" {
		t.Errorf("after moving C first: %s", got)
	}
	// Repeatedly inserting into the same gap eventually renumbers the column.
	for i := 0; i < 15; i++ {
		must[StatusResult](t)(s.MoveTask(slug, ids["B"], "todo", ids["A"], false))
		must[StatusResult](t)(s.MoveTask(slug, ids["A"], "todo", ids["B"], false))
	}
	if got := order("todo"); got != "CAB" {
		t.Errorf("after many moves: %s", got)
	}
	must[StatusResult](t)(s.MoveTask(slug, ids["A"], "in-progress", 0, false))
	if order("todo") != "CB" || order("in-progress") != "A" {
		t.Errorf("after moving A: todo=%s in-progress=%s", order("todo"), order("in-progress"))
	}
	if s.Rescan() {
		t.Error("renumbering left the index out of step with the files")
	}
}

func TestDeleteMovesTaskAndSubtasksToTrash(t *testing.T) {
	s := newStore(t)
	slug := must[string](t)(s.CreateProject(ProjectInput{Name: "P"}))
	parent := must[int](t)(s.CreateTask(slug, TaskInput{Title: "Parent"}))
	child := must[int](t)(s.CreateTask(slug, TaskInput{Title: "Child", Parent: parent}))
	must[int](t)(s.CreateTask(slug, TaskInput{Title: "Grandchild", Parent: child}))
	keep := must[int](t)(s.CreateTask(slug, TaskInput{Title: "Keep"}))
	if err := s.DeleteTask(slug, parent); err != nil {
		t.Fatal(err)
	}
	left, _ := os.ReadDir(filepath.Join(s.root, slug, "tasks"))
	trashed, _ := os.ReadDir(filepath.Join(s.root, ".trash", slug))
	if len(left) != 1 || len(trashed) != 3 {
		t.Errorf("left %d files, trashed %d", len(left), len(trashed))
	}
	if _, err := s.Task(slug, keep); err != nil {
		t.Error(err)
	}
	if s.Rescan() {
		t.Error("index out of step after delete")
	}
}

func TestDashboard(t *testing.T) {
	s := newStore(t)
	web := must[string](t)(s.CreateProject(ProjectInput{Name: "Website"}))
	home := must[string](t)(s.CreateProject(ProjectInput{Name: "Home"}))
	task := func(slug, title, status, deadline string, pri Priority, parent int) int {
		return must[int](t)(s.CreateTask(slug, TaskInput{Title: title, Status: status, Deadline: deadline, Priority: pri, Parent: parent}))
	}
	task(web, "Late", "in-progress", "2026-10-26", High, 0)
	task(home, "Today low", "todo", "2026-10-28", Low, 0)
	task(home, "Today high", "waiting", "2026-10-28", High, 0)
	parent := task(web, "Parent", "testing", "", Medium, 0)
	task(web, "Sub with deadline", "todo", "2026-11-01", Medium, parent) // Sunday: still this week
	task(web, "Next Monday", "todo", "2026-11-02", Medium, 0)
	task(web, "Finished", "done", "2026-10-20", High, 0)
	task(web, "No deadline", "todo", "", High, 0)
	archived := must[string](t)(s.CreateProject(ProjectInput{Name: "Old"}))
	task(archived, "Hidden", "todo", "2026-10-01", High, 0)
	s.UpdateProject(archived, ProjectPatch{Archived: ptr(true)})

	d := s.Dashboard()
	got := map[string][]string{}
	for _, g := range d.Groups {
		for _, it := range g.Tasks {
			got[g.Key] = append(got[g.Key], it.Title)
		}
	}
	want := map[string]string{"overdue": "Late", "today": "Today high,Today low", "week": "Sub with deadline", "later": "Next Monday"}
	for k, v := range want {
		if strings.Join(got[k], ",") != v {
			t.Errorf("group %s = %v, want %s", k, got[k], v)
		}
	}
	if late := d.Groups[0].Tasks[0]; late.DaysLate != 2 || late.Project.Name != "Website" {
		t.Errorf("overdue task = %+v", late)
	}
	if sub := d.Groups[2].Tasks[0]; sub.ParentTitle != "Parent" {
		t.Errorf("subtask parent title = %q", sub.ParentTitle)
	}
	if d.Overdue != 1 || d.DueToday != 2 || d.ActiveCount != 3 || d.ActiveProjects != 2 {
		t.Errorf("summary = %d overdue, %d today, %d active in %d projects", d.Overdue, d.DueToday, d.ActiveCount, d.ActiveProjects)
	}
	if len(d.Active) != 2 || d.Active[0].Name != "Home" || len(d.Active[1].Tasks) != 2 || d.Active[1].Tasks[0].Title != "Late" {
		t.Errorf("active = %+v", d.Active)
	}
	if len(d.Projects) != 2 {
		t.Fatalf("projects = %+v", d.Projects)
	}
	// Website: top-level only, so the subtask is not counted.
	if p := d.Projects[1]; p.Todo != 2 || p.Active != 2 || p.Done != 1 || p.Pct != 20 {
		t.Errorf("website summary = %+v", p)
	}
}

func TestSubtaskProgressCountsDirectChildren(t *testing.T) {
	s := newStore(t)
	slug := must[string](t)(s.CreateProject(ProjectInput{Name: "P"}))
	parent := must[int](t)(s.CreateTask(slug, TaskInput{Title: "Review copy"}))
	for i, title := range []string{"Hero", "About", "Services", "Footer"} {
		id := must[int](t)(s.CreateTask(slug, TaskInput{Title: title, Parent: parent}))
		if i < 2 {
			must[StatusResult](t)(s.SetDone(slug, id, true, false))
		}
		if title == "Services" {
			must[int](t)(s.CreateTask(slug, TaskInput{Title: "Pricing table", Parent: id}))
		}
	}
	d := must[TaskDetail](t)(s.Task(slug, parent))
	if d.SubDone != 2 || d.SubTotal != 4 || len(d.Subtasks) != 5 || d.Subtasks[3].Depth != 1 {
		t.Errorf("progress %d/%d, rows %+v", d.SubDone, d.SubTotal, d.Subtasks)
	}
}

func TestNotes(t *testing.T) {
	s := newStore(t)
	slug := must[string](t)(s.CreateProject(ProjectInput{Name: "P"}))
	s.AddNote(slug, 0, "first")
	s.now = func() time.Time { return testNow.Add(time.Hour) }
	s.AddNote(slug, 0, "second")
	if err := s.EditNote(slug, 0, 1, "first, edited"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddNote(slug, 0, "   "); err == nil {
		t.Error("empty note was accepted")
	}
	b := must[Board](t)(s.Board(slug))
	if len(b.Notes) != 2 || b.Notes[0].Text != "second" || b.Notes[1].Edited == nil || b.Notes[1].Edited.String() != "2026-10-28T15:00" {
		t.Errorf("notes = %+v", b.Notes)
	}
}

func TestLoadReportsProblemsWithoutFailing(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	write("p/project.yaml", "name: P\n")
	write("p/tasks/1-a.yaml", "id: 1\ntitle: A\nstatus: todo\n")
	write("p/tasks/1-copy.yaml", "id: 1\ntitle: Copy\n")
	write("p/tasks/2-b.yaml", "id: 2\ntitle: B\nparent: 99\nstatus: nonsense\n")
	write("p/tasks/3-broken.yaml", "id: 3\ntitle: [unclosed\n")
	write("p/tasks/4-a.sync-conflict-20261028.yaml", "id: 4\n")
	write("broken/project.yaml", "- not a mapping\n")

	s := must[*Store](t)(Open(root))
	if n := len(s.Problems()); n != 7 {
		t.Errorf("got %d problems, want 7: %+v", n, s.Problems())
	}
	b := must[Board](t)(s.Board("p"))
	if len(b.Columns) != 5 || len(b.Columns[0].Cards) != 2 {
		t.Errorf("board = %+v", b.Columns)
	}
}
