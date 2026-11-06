package main

import (
	"context"
	"fmt"
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
	libErr    string // why the saved library folder could not be opened
	store     *core.Store
	stopWatch context.CancelFunc
}

// newApp loads the settings and the library before the window opens. On
// Windows, Wails runs OnStartup alongside the page load, so anything the
// page asks for on its first call must already be in place.
func newApp() *App {
	a := &App{settings: loadSettings()}
	if path := a.settings.LibraryPath; path != "" {
		if s, err := core.Open(path); err != nil {
			a.libErr = err.Error()
		} else {
			a.store = s
		}
	}
	return a
}

func (a *App) startup(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ctx = ctx
	if a.store != nil {
		a.watchLocked()
	}
}

// watchLocked (re)starts the folder watcher for the current library.
func (a *App) watchLocked() {
	if a.stopWatch != nil {
		a.stopWatch()
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.stopWatch = cancel
	go a.store.Watch(ctx, func() { runtime.EventsEmit(a.ctx, "library-changed") })
}

func (a *App) lib() (*core.Store, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.store == nil {
		return nil, errNoLibrary
	}
	return a.store, nil
}

func (a *App) prefs() Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings
}

// --- Settings ---

// SettingsView is the settings plus why the library could not be opened.
type SettingsView struct {
	Settings
	LibraryError string `json:"libraryError"`
}

func (a *App) GetSettings() SettingsView {
	a.mu.Lock()
	defer a.mu.Unlock()
	return SettingsView{Settings: a.settings, LibraryError: a.libErr}
}

// SaveSettings stores everything except the library folder, which only
// ChooseLibrary changes. Default columns get their ids from their names.
func (a *App) SaveSettings(in Settings) (SettingsView, error) {
	cols := make([]core.Column, len(in.Projects.DefaultColumns))
	for i, c := range in.Projects.DefaultColumns {
		cols[i] = core.Column{ID: core.Slug(c.Name), Name: c.Name, Type: c.Type}
	}
	if err := core.ValidateColumns(cols); err != nil {
		return a.GetSettings(), err
	}
	switch in.Tasks.DefaultPriority {
	case core.Low, core.Medium, core.High:
	default:
		return a.GetSettings(), fmt.Errorf("unknown priority %q", in.Tasks.DefaultPriority)
	}
	in.Projects.DefaultColumns = cols
	a.mu.Lock()
	in.LibraryPath = a.settings.LibraryPath
	a.settings = in
	a.mu.Unlock()
	return a.GetSettings(), saveSettings(in)
}

// ChooseLibrary asks for the library folder. Cancelling keeps the current one.
func (a *App) ChooseLibrary() (SettingsView, error) {
	path, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:                "Choose the folder for your projects",
		DefaultDirectory:     a.prefs().LibraryPath,
		CanCreateDirectories: true,
	})
	if err != nil || path == "" {
		return a.GetSettings(), err
	}
	store, err := core.Open(path)
	if err != nil {
		return a.GetSettings(), err
	}
	a.mu.Lock()
	a.store, a.libErr = store, ""
	a.settings.LibraryPath = path
	a.watchLocked()
	s := a.settings
	a.mu.Unlock()
	return a.GetSettings(), saveSettings(s)
}

// RetryLibrary opens the saved library folder again, for when it was missing
// at start-up (a drive that was not connected).
func (a *App) RetryLibrary() (SettingsView, error) {
	path := a.prefs().LibraryPath
	store, err := core.Open(path)
	if err != nil {
		return a.GetSettings(), err
	}
	a.mu.Lock()
	a.store, a.libErr = store, ""
	a.watchLocked()
	a.mu.Unlock()
	return a.GetSettings(), nil
}

func (a *App) SetLastProject(slug string) error {
	a.mu.Lock()
	a.settings.LastProject = slug
	s := a.settings
	a.mu.Unlock()
	return saveSettings(s)
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
	d := a.prefs().Dashboard
	return s.Dashboard(core.DashboardOptions{IncludeSubtasks: d.IncludeSubtasks, IncludeMilestones: d.ShowMilestones}), nil
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

// CreateProject creates a project with the default columns from settings.
func (a *App) CreateProject(in core.ProjectInput) (string, error) {
	s, err := a.lib()
	if err != nil {
		return "", err
	}
	in.Columns = a.prefs().Projects.DefaultColumns
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

// --- Milestones ---

func (a *App) AddMilestone(slug string, in core.MilestoneInput) (int, error) {
	s, err := a.lib()
	if err != nil {
		return 0, err
	}
	return s.AddMilestone(slug, in)
}

func (a *App) UpdateMilestone(slug string, id int, in core.MilestonePatch) error {
	s, err := a.lib()
	if err != nil {
		return err
	}
	return s.UpdateMilestone(slug, id, in)
}

func (a *App) DeleteMilestone(slug string, id int) error {
	s, err := a.lib()
	if err != nil {
		return err
	}
	return s.DeleteMilestone(slug, id)
}

// --- Quick list ---

func (a *App) Inbox() ([]core.InboxItem, error) {
	s, err := a.lib()
	if err != nil {
		return nil, err
	}
	return s.Inbox()
}

func (a *App) AddInboxItem(text string) error {
	return a.withLib(func(s *core.Store) error { return s.AddInboxItem(text) })
}

func (a *App) SetInboxDone(id int, done bool) error {
	return a.withLib(func(s *core.Store) error { return s.SetInboxDone(id, done) })
}

func (a *App) DeleteInboxItem(id int) error {
	return a.withLib(func(s *core.Store) error { return s.DeleteInboxItem(id) })
}

func (a *App) ClearInboxDone() error {
	return a.withLib(func(s *core.Store) error { return s.ClearInboxDone() })
}

func (a *App) withLib(fn func(*core.Store) error) error {
	s, err := a.lib()
	if err != nil {
		return err
	}
	return fn(s)
}
