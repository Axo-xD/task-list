package core

import (
	"sort"
	"strings"
)

type ProjectRef struct {
	Slug  string `json:"slug"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type ProjectSummary struct {
	ProjectRef
	Description string `json:"description"`
	Deadline    *Date  `json:"deadline"`
	Archived    bool   `json:"archived"`
	Todo        int    `json:"todo"`
	Active      int    `json:"active"`
	Done        int    `json:"done"`
	Total       int    `json:"total"`
	Pct         int    `json:"pct"`
}

// ImportantTask is a row in Important tasks: a task, or (Kind "milestone") a
// milestone, whose ID is then the milestone's.
type ImportantTask struct {
	Kind        string     `json:"kind"` // task or milestone
	Project     ProjectRef `json:"project"`
	ID          int        `json:"id"`
	Title       string     `json:"title"`
	ParentTitle string     `json:"parentTitle"`
	StatusName  string     `json:"statusName"`
	Deadline    Date       `json:"deadline"`
	DaysLate    int        `json:"daysLate"`
	Priority    Priority   `json:"priority"`
	TasksDone   int        `json:"tasksDone"` // milestones: linked tasks
	TasksTotal  int        `json:"tasksTotal"`
}

// DashboardOptions are the user's dashboard settings.
type DashboardOptions struct {
	IncludeSubtasks   bool // subtasks with a deadline appear in Important tasks
	IncludeMilestones bool // open milestones appear in Important tasks
}

type MilestoneRef struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
}

// MilestoneView is a milestone with the progress of the tasks linked to it.
type MilestoneView struct {
	Milestone
	Done       bool `json:"done"`
	TasksDone  int  `json:"tasksDone"`
	TasksTotal int  `json:"tasksTotal"`
}

type ImportantGroup struct {
	Key   string          `json:"key"` // overdue, today, week, later
	Label string          `json:"label"`
	Tasks []ImportantTask `json:"tasks"`
}

type ActiveTask struct {
	ID         int    `json:"id"`
	Title      string `json:"title"`
	StatusName string `json:"statusName"`
	Tone       string `json:"tone"`
}

type ActiveProject struct {
	ProjectRef
	Tasks []ActiveTask `json:"tasks"`
}

type Dashboard struct {
	Today          Date             `json:"today"`
	Overdue        int              `json:"overdue"`
	DueToday       int              `json:"dueToday"`
	ActiveCount    int              `json:"activeCount"`
	ActiveProjects int              `json:"activeProjects"`
	Groups         []ImportantGroup `json:"groups"`
	Active         []ActiveProject  `json:"active"`
	Projects       []ProjectSummary `json:"projects"`
}

type SubtaskRow struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	Done  bool   `json:"done"`
	Depth int    `json:"depth"`
}

type Card struct {
	ID         int           `json:"id"`
	Title      string        `json:"title"`
	Priority   Priority      `json:"priority"`
	Deadline   *Date         `json:"deadline"`
	WaitingFor string        `json:"waitingFor"`
	Done       bool          `json:"done"`
	Completed  *Stamp        `json:"completed"`
	SubDone    int           `json:"subDone"`
	SubTotal   int           `json:"subTotal"`
	Subtasks   []SubtaskRow  `json:"subtasks"`
	Milestone  *MilestoneRef `json:"milestone"`
}

type BoardColumn struct {
	Column
	Tone  string `json:"tone"`
	Cards []Card `json:"cards"`
}

type Board struct {
	Project    ProjectSummary  `json:"project"`
	Start      Date            `json:"start"` // the project's creation date, for the timeline
	Columns    []BoardColumn   `json:"columns"`
	Milestones []MilestoneView `json:"milestones"` // by date
	Notes      []Note          `json:"notes"`
}

type TaskView struct {
	ID          int      `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	StatusName  string   `json:"statusName"`
	Tone        string   `json:"tone"`
	Done        bool     `json:"done"`
	Priority    Priority `json:"priority"`
	Deadline    *Date    `json:"deadline"`
	WaitingFor  string   `json:"waitingFor"`
	Created     Stamp    `json:"created"`
	Updated     Stamp    `json:"updated"`
	Completed   *Stamp   `json:"completed"`
	Milestone   int      `json:"milestone"` // 0 = none
}

type TaskRef struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
}

type TaskDetail struct {
	Project    ProjectRef     `json:"project"`
	Columns    []Column       `json:"columns"`
	Task       TaskView       `json:"task"`
	Ancestors  []TaskRef      `json:"ancestors"` // root first
	Subtasks   []SubtaskRow   `json:"subtasks"`  // whole tree, depth-first
	SubDone    int            `json:"subDone"`   // direct subtasks only
	SubTotal   int            `json:"subTotal"`
	Notes      []Note         `json:"notes"`
	Milestones []MilestoneRef `json:"milestones"` // the project's, for the milestone picker
}

func (s *Store) Dashboard(opts DashboardOptions) Dashboard {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	today := DateOf(now)
	endOfWeek := today.AddDate(0, 0, (7-int(today.Weekday()))%7) // through Sunday
	group := func(dl Date) (g, daysLate int) {
		switch {
		case dl.Before(today.Time):
			return 0, int(today.Sub(dl.Time).Hours()/24 + 0.5)
		case dl.Equal(today.Time):
			return 1, 0
		case !dl.After(endOfWeek):
			return 2, 0
		}
		return 3, 0
	}
	d := Dashboard{Today: today}
	groups := []ImportantGroup{
		{Key: "overdue", Label: "Overdue"},
		{Key: "today", Label: "Today"},
		{Key: "week", Label: "This week"},
		{Key: "later", Label: "Later"},
	}

	for _, ps := range s.sortedProjects(false) {
		ref := ps.ref()
		sum := ps.summary()
		d.Projects = append(d.Projects, sum)
		if sum.Active > 0 {
			d.ActiveProjects++
		}
		var active []*Task
		for _, t := range ps.tasks {
			col, _ := ps.p.column(t.Status)
			if col.Type == Done {
				continue
			}
			if col.Type == Active && t.Parent == nil {
				active = append(active, t)
			}
			if t.Deadline == nil || (t.Parent != nil && !opts.IncludeSubtasks) {
				continue
			}
			it := ImportantTask{Kind: "task", Project: ref, ID: t.ID, Title: t.Title, StatusName: col.Name, Deadline: *t.Deadline, Priority: t.Priority}
			if t.Parent != nil {
				it.ParentTitle = ps.tasks[*t.Parent].Title
			}
			g, late := group(*t.Deadline)
			it.DaysLate = late
			groups[g].Tasks = append(groups[g].Tasks, it)
		}
		if opts.IncludeMilestones {
			for _, m := range ps.milestoneViews() {
				if m.Done {
					continue
				}
				g, late := group(m.Date)
				groups[g].Tasks = append(groups[g].Tasks, ImportantTask{Kind: "milestone", Project: ref, ID: m.ID, Title: m.Title,
					StatusName: "Milestone", Deadline: m.Date, DaysLate: late, TasksDone: m.TasksDone, TasksTotal: m.TasksTotal})
			}
		}
		if len(active) > 0 {
			sort.Slice(active, func(i, j int) bool {
				ci, cj := ps.p.columnIndex(active[i].Status), ps.p.columnIndex(active[j].Status)
				if ci != cj {
					return ci < cj
				}
				return active[i].Position < active[j].Position
			})
			ap := ActiveProject{ProjectRef: ref}
			for _, t := range active {
				col, _ := ps.p.column(t.Status)
				ap.Tasks = append(ap.Tasks, ActiveTask{ID: t.ID, Title: t.Title, StatusName: col.Name, Tone: ps.tone(t.Status)})
			}
			d.Active = append(d.Active, ap)
			d.ActiveCount += len(active)
		}
	}
	for i := range groups {
		ts := groups[i].Tasks
		sort.Slice(ts, func(a, b int) bool {
			if !ts[a].Deadline.Equal(ts[b].Deadline.Time) {
				return ts[a].Deadline.Before(ts[b].Deadline.Time)
			}
			if ts[a].Kind != ts[b].Kind {
				return ts[a].Kind == "milestone" // a milestone heads its day
			}
			if ts[a].Priority.rank() != ts[b].Priority.rank() {
				return ts[a].Priority.rank() < ts[b].Priority.rank()
			}
			return strings.ToLower(ts[a].Title) < strings.ToLower(ts[b].Title)
		})
	}
	d.Overdue, d.DueToday = len(groups[0].Tasks), len(groups[1].Tasks)
	d.Groups = groups
	return d
}

// ListProjects returns all projects by name, archived ones included.
func (s *Store) ListProjects() []ProjectSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []ProjectSummary
	for _, ps := range s.sortedProjects(true) {
		out = append(out, ps.summary())
	}
	return out
}

func (s *Store) Board(slug string) (Board, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, err := s.project(slug)
	if err != nil {
		return Board{}, err
	}
	b := Board{Project: ps.summary(), Start: DateOf(ps.p.Created.Time), Milestones: ps.milestoneViews(), Notes: sortedNotes(ps.p.Notes)}
	byStatus := map[string][]Card{}
	for _, t := range ps.children(0) {
		c := Card{ID: t.ID, Title: t.Title, Priority: t.Priority, Deadline: t.Deadline, WaitingFor: string(t.WaitingFor), Done: ps.isDone(t), Completed: t.Completed}
		c.SubDone, c.SubTotal = ps.progress(t.ID)
		c.Subtasks = ps.subtaskRows(t.ID, 0)
		if t.Milestone != nil {
			if m, ok := ps.p.milestone(*t.Milestone); ok {
				c.Milestone = &MilestoneRef{ID: m.ID, Title: m.Title}
			}
		}
		byStatus[t.Status] = append(byStatus[t.Status], c)
	}
	for _, col := range ps.p.Columns {
		b.Columns = append(b.Columns, BoardColumn{Column: col, Tone: ps.tone(col.ID), Cards: byStatus[col.ID]})
	}
	return b, nil
}

func (s *Store) Task(slug string, id int) (TaskDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, t, err := s.task(slug, id)
	if err != nil {
		return TaskDetail{}, err
	}
	col, _ := ps.p.column(t.Status)
	d := TaskDetail{
		Project: ps.ref(),
		Columns: ps.p.Columns,
		Task: TaskView{
			ID: t.ID, Title: t.Title, Description: t.Description, Status: t.Status, StatusName: col.Name,
			Tone: ps.tone(t.Status), Done: col.Type == Done, Priority: t.Priority, Deadline: t.Deadline,
			WaitingFor: string(t.WaitingFor), Created: t.Created, Updated: t.Updated, Completed: t.Completed,
		},
		Subtasks: ps.subtaskRows(t.ID, 0),
		Notes:    sortedNotes(t.Notes),
	}
	if t.Milestone != nil {
		d.Task.Milestone = *t.Milestone
	}
	for _, m := range ps.p.Milestones {
		d.Milestones = append(d.Milestones, MilestoneRef{ID: m.ID, Title: m.Title})
	}
	d.SubDone, d.SubTotal = ps.progress(t.ID)
	for cur := t; cur.Parent != nil; {
		cur = ps.tasks[*cur.Parent]
		d.Ancestors = append([]TaskRef{{ID: cur.ID, Title: cur.Title}}, d.Ancestors...)
	}
	return d, nil
}

func (s *Store) sortedProjects(includeArchived bool) []*projectState {
	var out []*projectState
	for _, ps := range s.projects {
		if includeArchived || !ps.p.Archived {
			out = append(out, ps)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].p.Name) < strings.ToLower(out[j].p.Name)
	})
	return out
}

// milestoneViews returns the project's milestones by date, with the progress
// of the tasks linked to each.
func (ps *projectState) milestoneViews() []MilestoneView {
	var out []MilestoneView
	for _, m := range ps.p.Milestones {
		v := MilestoneView{Milestone: m, Done: m.Completed != nil}
		for _, t := range ps.tasks {
			if t.Milestone != nil && *t.Milestone == m.ID {
				v.TasksTotal++
				if ps.isDone(t) {
					v.TasksDone++
				}
			}
		}
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Date.Before(out[j].Date.Time) })
	return out
}

func (ps *projectState) ref() ProjectRef {
	return ProjectRef{Slug: ps.p.Slug, Name: ps.p.Name, Color: ps.p.Color}
}

// summary counts top-level tasks only, as shown on the board.
func (ps *projectState) summary() ProjectSummary {
	sum := ProjectSummary{ProjectRef: ps.ref(), Description: ps.p.Description, Deadline: ps.p.Deadline, Archived: ps.p.Archived}
	for _, t := range ps.tasks {
		if t.Parent != nil {
			continue
		}
		col, _ := ps.p.column(t.Status)
		switch col.Type {
		case NotStarted:
			sum.Todo++
		case Active:
			sum.Active++
		case Done:
			sum.Done++
		}
		sum.Total++
	}
	if sum.Total > 0 {
		sum.Pct = sum.Done * 100 / sum.Total
	}
	return sum
}

// progress counts direct subtasks only (a "2/4" on a card).
func (ps *projectState) progress(id int) (done, total int) {
	for _, c := range ps.children(id) {
		total++
		if ps.isDone(c) {
			done++
		}
	}
	return done, total
}

func (ps *projectState) subtaskRows(id, depth int) []SubtaskRow {
	var out []SubtaskRow
	for _, c := range ps.children(id) {
		out = append(out, SubtaskRow{ID: c.ID, Title: c.Title, Done: ps.isDone(c), Depth: depth})
		out = append(out, ps.subtaskRows(c.ID, depth+1)...)
	}
	return out
}

// tone picks the chip colour of a column: "todo", "done", or "active1".."active3"
// for the project's active columns in order.
func (ps *projectState) tone(status string) string {
	n := 0
	for _, c := range ps.p.Columns {
		if c.Type == Active {
			n++
		}
		if c.ID != status {
			continue
		}
		switch c.Type {
		case NotStarted:
			return "todo"
		case Done:
			return "done"
		}
		return "active" + string(rune('0'+(n-1)%3+1))
	}
	return "todo"
}

func sortedNotes(notes []Note) []Note {
	out := append([]Note{}, notes...)
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Created.Equal(out[j].Created.Time) {
			return out[i].Created.After(out[j].Created.Time)
		}
		return out[i].ID > out[j].ID
	})
	return out
}
