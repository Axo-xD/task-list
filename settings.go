package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Settings are per computer, so they live in the user's config folder
// (%APPDATA%\Tasks on Windows), not in the library.
type Settings struct {
	LibraryPath string `json:"libraryPath"`
	Theme       string `json:"theme"` // "light", "dark", or "" to follow Windows
	LastProject string `json:"lastProject"`
}

func settingsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Tasks", "settings.json"), nil
}

func loadSettings() Settings {
	var s Settings
	path, err := settingsPath()
	if err != nil {
		return s
	}
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &s)
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
