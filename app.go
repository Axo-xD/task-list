package main

import (
	"context"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"tasklist/core"
)

// App is the bridge between the window and the core. Every exported method
// can be called from JavaScript as window.go.main.App.<Method>.
type App struct {
	ctx       context.Context
	mu        sync.Mutex
	settings  Settings
	store     *core.Store
	stopWatch context.CancelFunc
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.settings = loadSettings()
	if a.settings.LibraryPath != "" {
		if err := a.openLibrary(a.settings.LibraryPath); err != nil {
			a.settings.LibraryPath = ""
		}
	}
}

func (a *App) openLibrary(path string) error {
	store, err := core.Open(path)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopWatch != nil {
		a.stopWatch()
	}
	watchCtx, cancel := context.WithCancel(a.ctx)
	a.store, a.stopWatch = store, cancel
	go store.Watch(watchCtx, func() { runtime.EventsEmit(a.ctx, "library-changed") })
	return nil
}

func (a *App) lib() (*core.Store, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.store == nil {
		return nil, errNoLibrary
	}
	return a.store, nil
}

// --- Settings ---

func (a *App) GetSettings() Settings { return a.settings }

// ChooseLibrary asks for the library folder. Cancelling keeps the current one.
func (a *App) ChooseLibrary() (Settings, error) {
	path, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:                "Choose the folder for your projects",
		DefaultDirectory:     a.settings.LibraryPath,
		CanCreateDirectories: true,
	})
	if err != nil || path == "" {
		return a.settings, err
	}
	if err := a.openLibrary(path); err != nil {
		return a.settings, err
	}
	a.settings.LibraryPath = path
	return a.settings, saveSettings(a.settings)
}

func (a *App) SetTheme(theme string) error {
	a.settings.Theme = theme
	return saveSettings(a.settings)
}

func (a *App) SetLastProject(slug string) error {
	a.settings.LastProject = slug
	return saveSettings(a.settings)
}

// ShowLibraryFolder opens the library (or one project's folder) in Explorer.
func (a *App) ShowLibraryFolder(slug string) error {
	s, err := a.lib()
	if err != nil {
		return err
	}
	path := filepath.Join(s.Root(), slug)
	cmd := "xdg-open"
	switch goruntime.GOOS {
	case "windows":
		cmd = "explorer"
	case "darwin":
		cmd = "open"
	}
	return exec.Command(cmd, path).Start()
}

func (a *App) Problems() []core.Problem {
	s, err := a.lib()
	if err != nil {
		return nil
	}
	return s.Problems()
}

// --- Queries ---

func (a *App) Dashboard() (core.Dashboard, error) {
	s, err := a.lib()
	if err != nil {
		return core.Dashboard{}, err
	}
	return s.Dashboard(), nil
}

func (a *App) Projects() ([]core.ProjectSummary, error) {
	s, err := a.lib()
	if err != nil {
		return nil, err
	}
	return s.ListProjects(), nil
}

func (a *App) Board(slug string) (core.Board, error) {
	s, err := a.lib()
	if err != nil {
		return core.Board{}, err
	}
	return s.Board(slug)
}

func (a *App) Task(slug string, id int) (core.TaskDetail, error) {
	s, err := a.lib()
	if err != nil {
		return core.TaskDetail{}, err
	}
	return s.Task(slug, id)
}

// --- Changes ---

func (a *App) CreateProject(in core.ProjectInput) (string, error) {
	s, err := a.lib()
	if err != nil {
		return "", err
	}
	return s.CreateProject(in)
}

func (a *App) UpdateProject(slug string, in core.ProjectPatch) error {
	s, err := a.lib()
	if err != nil {
		return err
	}
	return s.UpdateProject(slug, in)
}

func (a *App) CreateTask(slug string, in core.TaskInput) (int, error) {
	s, err := a.lib()
	if err != nil {
		return 0, err
	}
	return s.CreateTask(slug, in)
}

func (a *App) UpdateTask(slug string, id int, in core.TaskPatch) (core.StatusResult, error) {
	s, err := a.lib()
	if err != nil {
		return core.StatusResult{}, err
	}
	return s.UpdateTask(slug, id, in)
}

func (a *App) MoveTask(slug string, id int, status string, beforeID int, completeSubtasks bool) (core.StatusResult, error) {
	s, err := a.lib()
	if err != nil {
		return core.StatusResult{}, err
	}
	return s.MoveTask(slug, id, status, beforeID, completeSubtasks)
}

func (a *App) SetDone(slug string, id int, done, completeSubtasks bool) (core.StatusResult, error) {
	s, err := a.lib()
	if err != nil {
		return core.StatusResult{}, err
	}
	return s.SetDone(slug, id, done, completeSubtasks)
}

func (a *App) DeleteTask(slug string, id int) error {
	s, err := a.lib()
	if err != nil {
		return err
	}
	return s.DeleteTask(slug, id)
}

func (a *App) AddNote(slug string, taskID int, text string) error {
	s, err := a.lib()
	if err != nil {
		return err
	}
	return s.AddNote(slug, taskID, text)
}

func (a *App) EditNote(slug string, taskID, noteID int, text string) error {
	s, err := a.lib()
	if err != nil {
		return err
	}
	return s.EditNote(slug, taskID, noteID, text)
}
