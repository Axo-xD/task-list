package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A task that points at a column, milestone or parent this computer does
// not have yet (project.yaml still syncing, say) is shown with a stand-in,
// but an unrelated edit must not write the stand-in to the file.
func TestUnusableValuesStayInTheFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "p", "project.yaml"), "name: P\ncolumns:\n  - {id: todo, name: To do, type: not_started}\n  - {id: done, name: Done, type: done}\n")
	path := filepath.Join(root, "p", "tasks", "1-a.yaml")
	writeFile(t, path, "id: 1\ntitle: A\nstatus: review\npriority: urgent\nmilestone: 7\nparent: 99\n")
	s, err := openWithClock(root, func() time.Time { return testNow })
	if err != nil {
		t.Fatal(err)
	}
	if n := len(s.Problems()); n != 4 {
		t.Fatalf("got %d problems, want 4: %+v", n, s.Problems())
	}
	d := must[TaskDetail](t)(s.Task("p", 1))
	if d.Task.Status != "todo" || d.Task.Priority != Medium || d.Task.Milestone != 0 || len(d.Ancestors) != 0 {
		t.Errorf("stand-ins not used: %+v", d.Task)
	}
	if err := s.AddNote("p", 1, "an unrelated edit"); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	for _, want := range []string{"status: review\n", "priority: urgent\n", "milestone: 7\n", "parent: 99\n", "an unrelated edit"} {
		if !strings.Contains(got, want) {
			t.Errorf("file lost %q:\n%s", want, got)
		}
	}
	// The stand-ins are still what the app shows after its own save.
	if s.Rescan() {
		t.Error("own write seen as an outside change")
	}
	if d := must[TaskDetail](t)(s.Task("p", 1)); d.Task.Status != "todo" {
		t.Errorf("status = %q after save", d.Task.Status)
	}

	// Changing a field in the app replaces what the file said, for that field only.
	must[StatusResult](t)(s.UpdateTask("p", 1, TaskPatch{Status: ptr("done"), Priority: ptr(High)}))
	got = readFile(t, path)
	for _, want := range []string{"status: done\n", "priority: high\n", "milestone: 7\n", "parent: 99\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("after an explicit change, want %q:\n%s", want, got)
		}
	}
}

// Keys the app does not know, and comments, survive inside lists too.
func TestNestedUnknownKeysAndCommentsSurvive(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "p", "project.yaml")
	writeFile(t, path, `# my projects file
name: P
future_top: keep me
columns:
  # the first column
  - id: todo
    name: To do # shown on the board
    type: not_started
    wip_limit: 3
  - id: done
    name: Done
    type: done
milestones:
  - id: 1
    title: SAT
    date: 2026-11-20
    completed: null
    owner: kari
notes:
  - id: 1
    created: 2026-01-01T10:00
    edited: null
    text: first
    author: ola
`)
	s, err := openWithClock(root, func() time.Time { return testNow })
	if err != nil {
		t.Fatal(err)
	}
	if p := s.Problems(); len(p) != 0 {
		t.Fatalf("problems: %+v", p)
	}
	if err := s.AddNote("p", 0, "second"); err != nil {
		t.Fatal(err)
	}
	must[int](t)(s.AddMilestone("p", MilestoneInput{Title: "Delivery", Date: "2026-11-01"}))
	got := readFile(t, path)
	for _, want := range []string{"# my projects file", "future_top: keep me", "# the first column", "# shown on the board",
		"wip_limit: 3", "owner: kari", "author: ola", "text: second", "title: Delivery"} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q:\n%s", want, got)
		}
	}
	// The extras stay with their own item, also when the list is reordered
	// (Delivery now sorts before SAT, the new note before the first).
	if i, j := strings.Index(got, "title: SAT"), strings.Index(got, "owner: kari"); i < 0 || j < i || strings.Contains(got[i:j], "- id:") {
		t.Errorf("owner moved to another milestone:\n%s", got)
	}
	if i, j := strings.Index(got, "text: first"), strings.Index(got, "author: ola"); i < 0 || j < i || strings.Contains(got[i:j], "- id:") {
		t.Errorf("author moved to another note:\n%s", got)
	}
	// It reads back cleanly, and a removed item takes its extras with it.
	if s.Rescan() || len(s.Problems()) != 0 {
		t.Errorf("rescan changed or problems: %+v", s.Problems())
	}
	if err := s.DeleteMilestone("p", 1); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); strings.Contains(got, "owner: kari") {
		t.Errorf("extras of a deleted milestone were kept:\n%s", got)
	}

	// The quick list too.
	inbox := filepath.Join(root, inboxFile)
	writeFile(t, inbox, "items:\n  - id: 1\n    text: Ring Ola\n    done: false\n    created: 2026-11-06T09:12\n    tag: phone # call\n")
	s.Rescan()
	if err := s.AddInboxItem("Buy tape"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, inbox); !strings.Contains(got, "tag: phone # call") || !strings.Contains(got, "Buy tape") {
		t.Errorf("inbox extras lost:\n%s", got)
	}
}

// A deleted task can be put back from the trash, so its id is not given to
// a new task; the same goes for a milestone id a task still points to.
func TestIDsAreNotReused(t *testing.T) {
	s := newStore(t)
	slug := must[string](t)(s.CreateProject(ProjectInput{Name: "P"}))
	a := must[int](t)(s.CreateTask(slug, TaskInput{Title: "A"}))
	b := must[int](t)(s.CreateTask(slug, TaskInput{Title: "B"}))
	sub := must[int](t)(s.CreateTask(slug, TaskInput{Title: "B1", Parent: b}))
	if err := s.DeleteTask(slug, b); err != nil {
		t.Fatal(err)
	}
	if c := must[int](t)(s.CreateTask(slug, TaskInput{Title: "C"})); c <= sub {
		t.Errorf("new task got id %d; %d and %d are in the trash", c, b, sub)
	}
	// Also after a restart, and when the trashed file was renamed by hand.
	trash := filepath.Join(s.Root(), trashDir, slug)
	entries, _ := os.ReadDir(trash)
	if len(entries) != 2 {
		t.Fatalf("trash has %d files, want 2", len(entries))
	}
	writeFile(t, filepath.Join(trash, "old idea.yaml"), "id: 40\ntitle: Old idea\n")
	s2, err := openWithClock(s.Root(), s.now)
	if err != nil {
		t.Fatal(err)
	}
	if d := must[int](t)(s2.CreateTask(slug, TaskInput{Title: "D"})); d != 41 {
		t.Errorf("new task got id %d, want 41", d)
	}
	_ = a

	// A task whose file still names milestone 2 (it could not be unlinked,
	// or the milestone has not synced yet) blocks that id.
	m1 := must[int](t)(s2.AddMilestone(slug, MilestoneInput{Title: "M1", Date: "2026-11-20"}))
	writeFile(t, filepath.Join(s2.Root(), slug, "tasks", "50-x.yaml"), "id: 50\ntitle: X\nstatus: todo\nmilestone: 2\n")
	s2.Rescan()
	if m := must[int](t)(s2.AddMilestone(slug, MilestoneInput{Title: "M2", Date: "2026-11-21"})); m != 3 || m1 != 1 {
		t.Errorf("milestone ids %d, %d; want 1, 3", m1, m)
	}
}
