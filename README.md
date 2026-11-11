# Tasks

A personal to-do app for Windows with nested tasks, a Kanban board per
project, and a dated notes log. It is a desktop app (no browser, no server, no
account), and your data is plain YAML files in a folder you choose.

## Build it on Windows

You need:

1. **Go** — install from <https://go.dev/dl> (the Windows installer).
2. **The Wails tool** — in a terminal:

   ```
   go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.0
   ```

3. **WebView2** — already part of Windows 10 (updated) and Windows 11. Run
   `wails doctor` to check. It may also say npm is missing: ignore that, this
   app does not use npm.
4. **Git**, to clone this repository.

Then, in the repository folder:

```
wails build
```

The app is `build\bin\Tasks.exe`. Copy it anywhere and run it. On first start
it asks which folder to keep your projects in.

While working on the code, `wails dev` runs the app and reloads it when files
change. Run the tests with `go test ./...`.

## How the data is stored

```
<your library folder>/
  website-redesign/              one folder per project (copy, zip or move it)
    project.yaml                 name, colour, deadline, columns, milestones, description, notes
    tasks/
      4-review-homepage-copy.yaml
      5-hero-headline.yaml       subtasks are files too, pointing at their parent
  inbox.yaml                     the quick list (since 0.3)
  .trash/                        deleted tasks end up here
```

A task file:

```yaml
id: 4
title: Review homepage copy
status: in-progress          # a column id from project.yaml
priority: high               # low, medium or high
deadline: 2026-10-29
waiting_for: null
parent: null                 # id of the parent task for subtasks
position: 1000               # order within its column (or among its siblings)
created: 2026-10-14T09:00
updated: 2026-10-28T16:22
completed: null              # set when the task enters a Done-type column
milestone: 2                 # optional (since 0.3): id of a milestone in project.yaml
description: |-
  Go through all the page copy before the site goes to build.
notes:
  - id: 1
    created: 2026-10-28T16:20
    edited: null
    text: First pass done.
```

Milestones are listed in `project.yaml` (since 0.3; left out when there are
none):

```yaml
milestones:
  - id: 2
    title: SAT
    date: 2026-11-20
    description: Site acceptance test with the customer
    completed: null          # set when marked as reached
```

The quick list, `inbox.yaml` at the library root:

```yaml
items:
  - id: 1
    text: Ring Ola about the van
    done: false
    created: 2026-11-06T09:12
```

Rules:

- The files are the source of truth. The app reads them all at start-up and
  watches the folder, so edits made in a text editor or by a sync tool
  (OneDrive, Dropbox, Syncthing) show up straight away.
- The app never overwrites a file that changed since it last read it; it
  reloads instead. Keys it does not know about, and comments, are kept,
  also inside the lists (notes, columns, milestones, quick list items).
- A file it cannot read is listed under Settings → Files with problems and
  left alone until you fix it. Sync-conflict copies are listed there too.
- A task that names a column, milestone or parent that is not there (or a
  priority the app does not know) is listed there as well. It is shown with
  a stand-in (first column, no milestone, top level, medium), but the file
  keeps what it said until you change that field in the app, so nothing is
  lost while a sync tool is still bringing over `project.yaml`.
- Task ids are unique within a project and are not reused while the deleted
  task is still in `.trash`. The file name is only a label: the `id` inside
  the file is what counts, and renaming a task does not rename its file.
- Columns are edited in `project.yaml`. Each has an `id`, a `name` and a
  `type`: `not_started`, `active` or `done`. Tasks refer to the column `id`,
  so renaming a column only touches `project.yaml`.

### Compatibility between versions

Libraries stay readable in both directions:

- Fields are only ever **added**, never renamed or removed, and new fields
  are optional. A library written by an older version opens unchanged; there
  is nothing to convert. (A test opens a library written by 0.1 to prove it.)
- An older version opening a newer library ignores what it does not know
  (milestones, the quick list) and keeps those keys when it saves.

### Settings

Per-computer settings are in `%APPDATA%\Tasks\settings.json` and are edited
on the Settings page (the cog): theme, library folder, which dashboard
sections and extras to show, the columns for new projects, the default task
priority, and which board columns are collapsed. Settings missing from an
older file take their defaults.

## Code layout

- `core/` — reading and writing files, the index, the watcher, and the
  queries behind each screen. Plain Go, with tests.
- `app.go`, `main.go`, `settings.go` — the Wails desktop shell. Exported
  methods on `App` are called from the interface.
- `frontend/` — the interface: `index.html`, `style.css`, `app.js`. Plain
  HTML/CSS/JavaScript with no build step.
