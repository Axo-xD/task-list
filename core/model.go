// Package core reads and writes the task library (plain YAML files on disk)
// and answers the queries the UI needs. Files are the source of truth: the
// in-memory index is always rebuilt from them.
package core

import (
	"fmt"
	"time"

	"go.yaml.in/yaml/v3"
)

type ColumnType string

const (
	NotStarted ColumnType = "not_started"
	Active     ColumnType = "active"
	Done       ColumnType = "done"
)

type Priority string

const (
	Low    Priority = "low"
	Medium Priority = "medium"
	High   Priority = "high"
)

func (p Priority) rank() int {
	switch p {
	case High:
		return 0
	case Medium:
		return 1
	}
	return 2
}

func (p Priority) valid() bool { return p == Low || p == Medium || p == High }

type Column struct {
	ID   string     `yaml:"id" json:"id"`
	Name string     `yaml:"name" json:"name"`
	Type ColumnType `yaml:"type" json:"type"`
}

func DefaultColumns() []Column {
	return []Column{
		{ID: "todo", Name: "To do", Type: NotStarted},
		{ID: "in-progress", Name: "In progress", Type: Active},
		{ID: "waiting", Name: "Waiting", Type: Active},
		{ID: "testing", Name: "Testing", Type: Active},
		{ID: "done", Name: "Done", Type: Done},
	}
}

type Note struct {
	ID      int    `yaml:"id" json:"id"`
	Created Stamp  `yaml:"created" json:"created"`
	Edited  *Stamp `yaml:"edited" json:"edited"`
	Text    string `yaml:"text" json:"text"`
}

// Milestone is a dated goal in a project, such as a SAT or a delivery.
// Tasks link to it by id.
type Milestone struct {
	ID          int    `yaml:"id" json:"id"`
	Title       string `yaml:"title" json:"title"`
	Date        Date   `yaml:"date" json:"date"`
	Description string `yaml:"description,omitempty" json:"description"`
	Completed   *Stamp `yaml:"completed" json:"completed"`
}

// Project is the content of <library>/<slug>/project.yaml.
//
// Compatibility: fields are only ever added, and new ones are optional
// (omitempty), so files written by older versions load unchanged and older
// versions keep the new keys when they save.
type Project struct {
	Name        string      `yaml:"name"`
	Color       string      `yaml:"color"`
	Deadline    *Date       `yaml:"deadline"`
	Archived    bool        `yaml:"archived"`
	Created     Stamp       `yaml:"created"`
	Columns     []Column    `yaml:"columns"`
	Milestones  []Milestone `yaml:"milestones,omitempty"` // since 0.3
	Description string      `yaml:"description"`
	Notes       []Note      `yaml:"notes"`

	Slug string `yaml:"-"` // folder name; the project's id
	file fileInfo
}

func (p *Project) column(id string) (Column, bool) {
	for _, c := range p.Columns {
		if c.ID == id {
			return c, true
		}
	}
	return Column{}, false
}

func (p *Project) columnIndex(id string) int {
	for i, c := range p.Columns {
		if c.ID == id {
			return i
		}
	}
	return len(p.Columns)
}

func (p *Project) firstOfType(t ColumnType) string {
	for _, c := range p.Columns {
		if c.Type == t {
			return c.ID
		}
	}
	return ""
}

// Task is the content of <library>/<slug>/tasks/<id>-<title>.yaml.
type Task struct {
	ID          int      `yaml:"id"`
	Title       string   `yaml:"title"`
	Status      string   `yaml:"status"`
	Priority    Priority `yaml:"priority"`
	Deadline    *Date    `yaml:"deadline"`
	WaitingFor  optText  `yaml:"waiting_for"`
	Parent      *int     `yaml:"parent"`
	Position    int      `yaml:"position"`
	Created     Stamp    `yaml:"created"`
	Updated     Stamp    `yaml:"updated"`
	Completed   *Stamp   `yaml:"completed"`
	Milestone   *int     `yaml:"milestone,omitempty"` // since 0.3
	Description string   `yaml:"description"`
	Notes       []Note   `yaml:"notes"`

	file fileInfo
	keep kept
}

// kept holds values from the file that could not be used when it was read
// (a column or milestone this project.yaml does not have, an unknown
// priority, a missing parent). The task is shown with a stand-in, but the
// file keeps what it said until that field is changed in the app: the
// missing piece may simply not have synced yet.
type kept struct {
	status    *string
	priority  *Priority
	milestone *int
	parent    *int
}

// onDisk is the task as it is written to its file: the kept values in place
// of their stand-ins.
func (t *Task) onDisk() *Task {
	d := *t
	if t.keep.status != nil {
		d.Status = *t.keep.status
	}
	if t.keep.priority != nil {
		d.Priority = *t.keep.priority
	}
	if t.keep.milestone != nil {
		d.Milestone = t.keep.milestone
	}
	if t.keep.parent != nil {
		d.Parent = t.keep.parent
	}
	return &d
}

func (p *Project) milestone(id int) (*Milestone, bool) {
	for i := range p.Milestones {
		if p.Milestones[i].ID == id {
			return &p.Milestones[i], true
		}
	}
	return nil, false
}

// ValidateColumns checks a column list: unique non-empty ids and names,
// known types, and at least one not-started and one done column (ticking a
// task done or undone needs both).
func ValidateColumns(cols []Column) error {
	seen := map[string]bool{}
	var open, done bool
	for _, c := range cols {
		switch {
		case c.ID == "" || c.Name == "":
			return fmt.Errorf("every column needs a name")
		case seen[c.ID]:
			return fmt.Errorf("two columns are both called %q", c.Name)
		case c.Type != NotStarted && c.Type != Active && c.Type != Done:
			return fmt.Errorf("column %q has an unknown type %q", c.Name, c.Type)
		}
		seen[c.ID] = true
		open = open || c.Type == NotStarted
		done = done || c.Type == Done
	}
	if !open || !done {
		return fmt.Errorf("there must be at least one not-started column and one done column")
	}
	return nil
}

func (t *Task) parentID() int {
	if t.Parent == nil {
		return 0
	}
	return *t.Parent
}

// Date is a calendar date without time zone, written as 2006-01-02.
type Date struct{ time.Time }

const dateLayout = "2006-01-02"

func ParseDate(s string) (Date, error) {
	t, err := time.ParseInLocation(dateLayout, s, time.Local)
	if err != nil {
		return Date{}, fmt.Errorf("invalid date %q, expected YYYY-MM-DD", s)
	}
	return Date{t}, nil
}

func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{time.Date(y, m, d, 0, 0, 0, 0, time.Local)}
}

func (d Date) String() string { return d.Format(dateLayout) }

// MarshalYAML writes the date unquoted, as people would type it.
func (d Date) MarshalYAML() (any, error) {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!timestamp", Value: d.String()}, nil
}

func (d *Date) UnmarshalYAML(n *yaml.Node) error {
	v, err := ParseDate(n.Value)
	if err != nil {
		return err
	}
	*d = v
	return nil
}

func (d Date) MarshalJSON() ([]byte, error) { return []byte(`"` + d.String() + `"`), nil }

// Stamp is a local date and time to the minute, written as 2006-01-02T15:04.
type Stamp struct{ time.Time }

const stampLayout = "2006-01-02T15:04"

func StampOf(t time.Time) Stamp { return Stamp{t.Local().Truncate(time.Minute)} }

func (s Stamp) String() string { return s.Format(stampLayout) }

func (s Stamp) MarshalYAML() (any, error) { return s.String(), nil }

func (s *Stamp) UnmarshalYAML(n *yaml.Node) error {
	// Accept the app's own format, and also a bare date or a full
	// timestamp in case someone edits the file by hand.
	for _, layout := range []string{stampLayout, "2006-01-02 15:04", time.RFC3339, dateLayout} {
		if t, err := time.ParseInLocation(layout, n.Value, time.Local); err == nil {
			*s = StampOf(t)
			return nil
		}
	}
	return fmt.Errorf("invalid time %q, expected YYYY-MM-DDTHH:MM", n.Value)
}

func (s Stamp) MarshalJSON() ([]byte, error) { return []byte(`"` + s.String() + `"`), nil }

// optText is a string written as null when empty.
type optText string

func (o optText) MarshalYAML() (any, error) {
	if o == "" {
		return nil, nil
	}
	return string(o), nil
}
