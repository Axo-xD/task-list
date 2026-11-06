package core

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

type MilestoneInput struct {
	Title       string `json:"title"`
	Date        string `json:"date"` // YYYY-MM-DD, required
	Description string `json:"description"`
}

// MilestonePatch changes only the fields that are set.
type MilestonePatch struct {
	Title       *string `json:"title"`
	Date        *string `json:"date"`
	Description *string `json:"description"`
	Done        *bool   `json:"done"`
}

func (s *Store) AddMilestone(slug string, in MilestoneInput) (int, error) {
	m := Milestone{Title: strings.TrimSpace(in.Title), Description: strings.TrimSpace(in.Description)}
	if m.Title == "" {
		return 0, errors.New("a milestone needs a title")
	}
	d, err := ParseDate(strings.TrimSpace(in.Date))
	if err != nil {
		return 0, errors.New("a milestone needs a date")
	}
	m.Date = d
	err = s.editMilestones(slug, func(p *Project) error {
		for _, x := range p.Milestones {
			m.ID = max(m.ID, x.ID)
		}
		m.ID++
		p.Milestones = append(p.Milestones, m)
		return nil
	})
	return m.ID, err
}

func (s *Store) UpdateMilestone(slug string, id int, in MilestonePatch) error {
	return s.editMilestones(slug, func(p *Project) error {
		m, ok := p.milestone(id)
		if !ok {
			return fmt.Errorf("milestone %d not found", id)
		}
		if in.Title != nil {
			if strings.TrimSpace(*in.Title) == "" {
				return errors.New("a milestone needs a title")
			}
			m.Title = strings.TrimSpace(*in.Title)
		}
		if in.Date != nil {
			d, err := ParseDate(strings.TrimSpace(*in.Date))
			if err != nil {
				return errors.New("a milestone needs a date")
			}
			m.Date = d
		}
		if in.Description != nil {
			m.Description = strings.TrimSpace(*in.Description)
		}
		if in.Done != nil {
			switch {
			case !*in.Done:
				m.Completed = nil
			case m.Completed == nil:
				now := StampOf(s.now())
				m.Completed = &now
			}
		}
		return nil
	})
}

// DeleteMilestone removes a milestone and unlinks the tasks that pointed to it.
func (s *Store) DeleteMilestone(slug string, id int) error {
	err := s.editMilestones(slug, func(p *Project) error {
		for i, m := range p.Milestones {
			if m.ID == id {
				p.Milestones = append(p.Milestones[:i:i], p.Milestones[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("milestone %d not found", id)
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, err := s.project(slug)
	if err != nil {
		return err
	}
	for _, t := range ps.tasks {
		if t.Milestone != nil && *t.Milestone == id {
			next := *t
			next.Milestone = nil
			if err := s.writeTask(ps, t, &next, false); err != nil {
				return err
			}
		}
	}
	return nil
}

// editMilestones edits a copy of the project, keeps milestones in date
// order, and saves it.
func (s *Store) editMilestones(slug string, edit func(*Project) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, err := s.project(slug)
	if err != nil {
		return err
	}
	p := *ps.p
	p.Milestones = append([]Milestone{}, ps.p.Milestones...)
	if err := edit(&p); err != nil {
		return err
	}
	sort.SliceStable(p.Milestones, func(i, j int) bool { return p.Milestones[i].Date.Before(p.Milestones[j].Date.Time) })
	if err := s.saveProject(&p); err != nil {
		return err
	}
	*ps.p = p
	return nil
}
