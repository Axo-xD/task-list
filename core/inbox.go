package core

import (
	"errors"
	"fmt"
	"strings"
)

// inboxFile holds the quick list: loose to-dos that belong to no project.
// It sits at the library root, so it moves with the library. Added in 0.3;
// older versions ignore it.
const inboxFile = "inbox.yaml"

type InboxItem struct {
	ID      int    `yaml:"id" json:"id"`
	Text    string `yaml:"text" json:"text"`
	Done    bool   `yaml:"done" json:"done"`
	Created Stamp  `yaml:"created" json:"created"`
}

type Inbox struct {
	Items []InboxItem `yaml:"items"`

	file fileInfo
}

// Inbox returns the quick list, oldest first.
func (s *Store) Inbox() ([]InboxItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inbox == nil {
		return nil, errInboxUnreadable
	}
	return append([]InboxItem{}, s.inbox.Items...), nil
}

var errInboxUnreadable = errors.New("inbox.yaml in the library folder can't be read; fix or delete it first")

func (s *Store) AddInboxItem(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("write something first")
	}
	return s.editInbox(func(items []InboxItem) ([]InboxItem, error) {
		id := 0
		for _, it := range items {
			id = max(id, it.ID)
		}
		return append(items, InboxItem{ID: id + 1, Text: text, Created: StampOf(s.now())}), nil
	})
}

func (s *Store) SetInboxDone(id int, done bool) error {
	return s.editInbox(func(items []InboxItem) ([]InboxItem, error) {
		for i := range items {
			if items[i].ID == id {
				items[i].Done = done
				return items, nil
			}
		}
		return nil, fmt.Errorf("item %d not found", id)
	})
}

func (s *Store) DeleteInboxItem(id int) error {
	return s.editInbox(func(items []InboxItem) ([]InboxItem, error) {
		for i := range items {
			if items[i].ID == id {
				return append(items[:i], items[i+1:]...), nil
			}
		}
		return nil, fmt.Errorf("item %d not found", id)
	})
}

// ClearInboxDone removes every ticked item.
func (s *Store) ClearInboxDone() error {
	return s.editInbox(func(items []InboxItem) ([]InboxItem, error) {
		var out []InboxItem
		for _, it := range items {
			if !it.Done {
				out = append(out, it)
			}
		}
		return out, nil
	})
}

func (s *Store) editInbox(edit func([]InboxItem) ([]InboxItem, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inbox == nil {
		return errInboxUnreadable
	}
	next := *s.inbox
	items, err := edit(append([]InboxItem{}, s.inbox.Items...))
	if err != nil {
		return err
	}
	next.Items = items
	if err := writeYAML(&next.file, &next); err != nil {
		s.reloadLocked()
		return err
	}
	s.seen[next.file.path] = next.file.hash
	*s.inbox = next
	return nil
}
