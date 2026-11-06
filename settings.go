package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"tasklist/core"
)

// Settings are per computer, so they live in the user's config folder
// (%APPDATA%\Tasks on Windows), not in the library. Settings missing from an
// older settings.json take their defaults (see defaultSettings).
type Settings struct {
	LibraryPath string `json:"libraryPath"`
	Theme       string `json:"theme"` // "light", "dark", or "" to follow Windows
	LastProject string `json:"lastProject"`
	QuickList   bool   `json:"quickList"` // the quick list side bar is open
	// Collapsed board columns, by project folder name.
	Collapsed map[string][]string `json:"collapsed"`

	Dashboard DashboardSettings `json:"dashboard"`
	Projects  ProjectSettings   `json:"projects"`
	Tasks     TaskSettings      `json:"tasks"`
}

type DashboardSettings struct {
	ShowImportant   bool `json:"showImportant"`
	ShowActive      bool `json:"showActive"`
	ShowProjects    bool `json:"showProjects"`
	ShowMilestones  bool `json:"showMilestones"`  // in Important tasks
	ShowWeekNumbers bool `json:"showWeekNumbers"` // ISO weeks
	IncludeSubtasks bool `json:"includeSubtasks"` // subtasks with a deadline in Important tasks
}

type ProjectSettings struct {
	DefaultColumns []core.Column `json:"defaultColumns"` // for new projects
	ShowTimeline   bool          `json:"showTimeline"`   // milestone timeline on the board
}

type TaskSettings struct {
	DefaultPriority      core.Priority `json:"defaultPriority"`
	ShowMilestoneOnCards bool          `json:"showMilestoneOnCards"`
}

func defaultSettings() Settings {
	return Settings{
		Dashboard: DashboardSettings{ShowImportant: true, ShowActive: true, ShowProjects: true,
			ShowMilestones: true, ShowWeekNumbers: true, IncludeSubtasks: true},
		Projects: ProjectSettings{DefaultColumns: core.DefaultColumns(), ShowTimeline: true},
		Tasks:    TaskSettings{DefaultPriority: core.Medium, ShowMilestoneOnCards: true},
	}
}

func settingsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Tasks", "settings.json"), nil
}

// loadSettings reads settings.json over the defaults, so keys the file does
// not have keep their default value.
func loadSettings() Settings {
	s := defaultSettings()
	path, err := settingsPath()
	if err != nil {
		return s
	}
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &s)
	}
	if core.ValidateColumns(s.Projects.DefaultColumns) != nil {
		s.Projects.DefaultColumns = core.DefaultColumns()
	}
	return s
}

func saveSettings(s Settings) error {
	path, err := settingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

var errNoLibrary = errors.New("no library folder chosen yet")
