package core

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// copyDir copies a test library so tests can change it.
func copyDir(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// A library written by version 0.1 opens without problems, and saving it
// with this version adds none of the new keys unless they are used.
func TestOpensVersion01Library(t *testing.T) {
	root := copyDir(t, "testdata/v0.1-library")
	s := must[*Store](t)(openWithClock(root, func() time.Time { return testNow }))
	if p := s.Problems(); len(p) != 0 {
		t.Fatalf("problems: %+v", p)
	}
	b := must[Board](t)(s.Board("website-redesign"))
	if b.Project.Total != 9 || len(b.Notes) != 3 || len(b.Milestones) != 0 {
		t.Errorf("board: total %d, %d notes, %d milestones", b.Project.Total, len(b.Notes), len(b.Milestones))
	}
	d := must[TaskDetail](t)(s.Task("website-redesign", 4))
	if d.SubTotal != 5 || len(d.Subtasks) != 6 || len(d.Notes) != 1 {
		t.Errorf("task 4: %d/%d subtasks, %d rows, %d notes", d.SubDone, d.SubTotal, len(d.Subtasks), len(d.Notes))
	}
	if items, err := s.Inbox(); err != nil || len(items) != 0 {
		t.Errorf("inbox = %v, %v", items, err)
	}

	must[StatusResult](t)(s.UpdateTask("website-redesign", 4, TaskPatch{Priority: ptr(Low)}))
	s.UpdateProject("website-redesign", ProjectPatch{Description: ptr("Changed")})
	for _, f := range []string{"website-redesign/tasks/4-review-homepage-copy.yaml", "website-redesign/project.yaml"} {
		if got := readFile(t, filepath.Join(root, f)); strings.Contains(got, "milestone") {
			t.Errorf("%s gained a milestone key:\n%s", f, got)
		}
	}
	if _, err := os.Stat(filepath.Join(root, inboxFile)); err == nil {
		t.Error("inbox.yaml was created without being used")
	}
}

func TestMilestones(t *testing.T) {
	s := newStore(t)
	slug := must[string](t)(s.CreateProject(ProjectInput{Name: "P"}))
	sat := must[int](t)(s.AddMilestone(slug, MilestoneInput{Title: "SAT", Date: "2026-11-20"}))
	fat := must[int](t)(s.AddMilestone(slug, MilestoneInput{Title: "FAT", Date: "2026-10-27"}))
	if _, err := s.AddMilestone(slug, MilestoneInput{Title: "No date"}); err == nil {
		t.Error("milestone without a date was accepted")
	}
	a := must[int](t)(s.CreateTask(slug, TaskInput{Title: "A"}))
	b := must[int](t)(s.CreateTask(slug, TaskInput{Title: "B"}))
	must[StatusResult](t)(s.UpdateTask(slug, a, TaskPatch{Milestone: ptr(sat)}))
	must[StatusResult](t)(s.UpdateTask(slug, b, TaskPatch{Milestone: ptr(sat)}))
	must[StatusResult](t)(s.SetDone(slug, a, true, false))
	if _, err := s.UpdateTask(slug, b, TaskPatch{Milestone: ptr(99)}); err == nil {
		t.Error("linked to a missing milestone")
	}

	board := must[Board](t)(s.Board(slug))
	if len(board.Milestones) != 2 || board.Milestones[0].Title != "FAT" {
		t.Fatalf("milestones not in date order: %+v", board.Milestones)
	}
	if m := board.Milestones[1]; m.TasksDone != 1 || m.TasksTotal != 2 {
		t.Errorf("SAT progress %d/%d", m.TasksDone, m.TasksTotal)
	}
	if c := board.Columns[0].Cards[0]; c.Milestone == nil || c.Milestone.Title != "SAT" {
		t.Errorf("card milestone = %+v", c.Milestone)
	}

	// On the dashboard only when asked for; FAT is overdue.
	if d := s.Dashboard(DashboardOptions{}); len(d.Groups[0].Tasks) != 0 {
		t.Errorf("milestones shown without the option: %+v", d.Groups[0].Tasks)
	}
	d := s.Dashboard(DashboardOptions{IncludeMilestones: true})
	if g := d.Groups[0].Tasks; len(g) != 1 || g[0].Kind != "milestone" || g[0].DaysLate != 1 {
		t.Errorf("overdue = %+v", g)
	}
	if err := s.UpdateMilestone(slug, fat, MilestonePatch{Done: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	if d := s.Dashboard(DashboardOptions{IncludeMilestones: true}); d.Overdue != 0 {
		t.Error("a done milestone is still overdue")
	}

	// Deleting a milestone unlinks its tasks, and the keys leave the files.
	if err := s.DeleteMilestone(slug, sat); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMilestone(slug, fat); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, s.taskPath(slug, b)); strings.Contains(got, "milestone") {
		t.Errorf("task still linked:\n%s", got)
	}
	if got := readFile(t, s.projects[slug].p.file.path); strings.Contains(got, "milestones") {
		t.Errorf("project still lists milestones:\n%s", got)
	}
	if s.Rescan() {
		t.Error("index out of step with the files")
	}
}

func TestMissingMilestoneIsReported(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "p", "tasks"), 0o755)
	os.WriteFile(filepath.Join(root, "p", "project.yaml"), []byte("name: P\ncolumns:\n  - {id: todo, name: To do, type: not_started}\n  - {id: done, name: Done, type: done}\n"), 0o644)
	os.WriteFile(filepath.Join(root, "p", "tasks", "1-a.yaml"), []byte("id: 1\ntitle: A\nstatus: todo\nmilestone: 3\n"), 0o644)
	s := must[*Store](t)(Open(root))
	if p := s.Problems(); len(p) != 1 || !strings.Contains(p[0].Message, "milestone 3") {
		t.Errorf("problems = %+v", p)
	}
}

func TestInbox(t *testing.T) {
	s := newStore(t)
	for _, text := range []string{"Buy milk", "Call Ola", "  "} {
		s.AddInboxItem(text)
	}
	items := must[[]InboxItem](t)(s.Inbox())
	if len(items) != 2 || items[1].Text != "Call Ola" {
		t.Fatalf("items = %+v", items)
	}
	if err := s.SetInboxDone(items[0].ID, true); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(s.root, inboxFile))
	if !strings.Contains(got, "text: Buy milk\n    done: true\n") {
		t.Errorf("inbox.yaml:\n%s", got)
	}
	if err := s.ClearInboxDone(); err != nil {
		t.Fatal(err)
	}
	if items := must[[]InboxItem](t)(s.Inbox()); len(items) != 1 || items[0].Text != "Call Ola" {
		t.Errorf("after clearing: %+v", items)
	}
	if s.Rescan() {
		t.Error("own inbox write reported as an outside change")
	}
	// An edit made in a text editor is picked up.
	os.WriteFile(filepath.Join(s.root, inboxFile), []byte("items:\n  - id: 5\n    text: Edited by hand\n    done: false\n    created: 2026-11-01T10:00\n"), 0o644)
	if !s.Rescan() {
		t.Fatal("hand edit not noticed")
	}
	if items := must[[]InboxItem](t)(s.Inbox()); len(items) != 1 || items[0].Text != "Edited by hand" {
		t.Errorf("after hand edit: %+v", items)
	}
}

func TestDashboardCanLeaveOutSubtasks(t *testing.T) {
	s := newStore(t)
	slug := must[string](t)(s.CreateProject(ProjectInput{Name: "P"}))
	parent := must[int](t)(s.CreateTask(slug, TaskInput{Title: "Parent"}))
	must[int](t)(s.CreateTask(slug, TaskInput{Title: "Sub", Parent: parent, Deadline: "2026-10-29"}))
	if d := s.Dashboard(DashboardOptions{}); len(d.Groups[2].Tasks) != 0 {
		t.Errorf("subtask shown: %+v", d.Groups[2].Tasks)
	}
	if d := s.Dashboard(DashboardOptions{IncludeSubtasks: true}); len(d.Groups[2].Tasks) != 1 {
		t.Errorf("subtask missing: %+v", d.Groups)
	}
}

func TestCustomColumns(t *testing.T) {
	s := newStore(t)
	cols := []Column{{ID: "backlog", Name: "Backlog", Type: NotStarted}, {ID: "doing", Name: "Doing", Type: Active}, {ID: "shipped", Name: "Shipped", Type: Done}}
	slug := must[string](t)(s.CreateProject(ProjectInput{Name: "P", Columns: cols}))
	id := must[int](t)(s.CreateTask(slug, TaskInput{Title: "A"}))
	must[StatusResult](t)(s.SetDone(slug, id, true, false))
	if d := must[TaskDetail](t)(s.Task(slug, id)); d.Task.Status != "shipped" {
		t.Errorf("status = %s", d.Task.Status)
	}
	for _, bad := range [][]Column{
		{{ID: "a", Name: "A", Type: Active}, {ID: "d", Name: "D", Type: Done}},
		{{ID: "a", Name: "A", Type: NotStarted}, {ID: "a", Name: "A", Type: Done}},
		{{ID: "a", Name: "A", Type: NotStarted}, {ID: "d", Name: "D", Type: "later"}},
	} {
		if _, err := s.CreateProject(ProjectInput{Name: "Bad", Columns: bad}); err == nil {
			t.Errorf("accepted columns %+v", bad)
		}
	}
}

func TestOpenRefusesMissingFolder(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "unplugged")
	if _, err := Open(missing); err == nil {
		t.Fatal("opened a missing folder")
	}
	if _, err := os.Stat(missing); err == nil {
		t.Error("the missing folder was created")
	}
}

func TestSlugKeepsNordicLetters(t *testing.T) {
	for in, want := range map[string]string{"Tromsø": "tromso", "Bodø havn": "bodo-havn", "Få OPCUA opp å gå på PLC": "fa-opcua-opp-a-ga-pa-plc", "Ærlig talt": "aerlig-talt"} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}
