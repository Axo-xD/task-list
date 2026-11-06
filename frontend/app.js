'use strict';

// Calls into Go (app.go). Each returns a Promise; Go errors reject it with a message.
const api = new Proxy({}, { get: (_, name) => (...args) => window.go.main.App[name](...args) });

const appEl = document.getElementById('app');
const view = document.getElementById('view');
const quickEl = document.getElementById('quick');
const dlg = document.getElementById('dlg');
const toastEl = document.getElementById('toast');

const state = {
  settings: null,      // SettingsView from Go (settings.go)
  routeKey: '',
  data: null,          // what the current view was drawn from
  drafts: {},          // unsent note text, by notes key
  editingNote: null,   // { key, id, text }
  editTitle: null,     // text while renaming the task
  editDesc: null,      // text while editing the description
  expanded: new Set(), // board cards showing their subtasks
  colsDraft: null,     // default columns being edited on the settings page
  quickDraft: '',      // unsent quick list item
  pendingRefresh: false,
};

const PALETTE = ['#2F5BEA', '#7C3AED', '#D97706', '#0E9384', '#DC2626', '#DB2777', '#4B5563', '#16A34A'];
const PRIORITY = { high: 'High', medium: 'Medium', low: 'Low' };
const COLUMN_TYPES = { not_started: 'Not started', active: 'Active', done: 'Done' };
const DEFAULT_COLUMNS = [
  { name: 'To do', type: 'not_started' }, { name: 'In progress', type: 'active' }, { name: 'Waiting', type: 'active' },
  { name: 'Testing', type: 'active' }, { name: 'Done', type: 'done' },
];

// ---------- Helpers ----------

const esc = (v) => String(v ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const color = (c) => (/^#[0-9a-fA-F]{3,8}$/.test(c || '') ? c : '#5B6270');
const plural = (n, word) => `${n} ${word}${n === 1 ? '' : 's'}`;
const taskHref = (slug, id) => `#/p/${encodeURIComponent(slug)}/t/${id}`;
const boardHref = (slug) => `#/p/${encodeURIComponent(slug)}`;
const prefs = () => state.settings;

const icon = {
  cal: (s = 14) => `<svg aria-hidden="true" width="${s}" height="${s}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="18" rx="2"></rect><path d="M16 2v4M8 2v4M3 10h18"></path></svg>`,
  clock: `<svg aria-hidden="true" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"></circle><path d="M12 7v5l3 2"></path></svg>`,
  plus: (s = 14) => `<svg aria-hidden="true" width="${s}" height="${s}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><path d="M12 5v14M5 12h14"></path></svg>`,
  check: `<svg aria-hidden="true" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="var(--done-check)" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" style="flex: none; margin-top: 1px"><path d="M5 12l5 5L20 7"></path></svg>`,
  moon: `<svg aria-hidden="true" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z"></path></svg>`,
  sun: `<svg aria-hidden="true" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="4"></circle><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"></path></svg>`,
  diamond: (s = 12) => `<svg aria-hidden="true" width="${s}" height="${s}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linejoin="round"><path d="M12 2.5l9.5 9.5-9.5 9.5L2.5 12z"></path></svg>`,
  chevron: (open) => `<svg aria-hidden="true" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">${open ? '<path d="M6 9l6 6 6-6"></path>' : '<path d="M9 6l6 6-6 6"></path>'}</svg>`,
  close: `<svg aria-hidden="true" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"></path></svg>`,
  up: `<svg aria-hidden="true" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M6 15l6-6 6 6"></path></svg>`,
  down: `<svg aria-hidden="true" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M6 9l6 6 6-6"></path></svg>`,
};

function parseDate(s) { // "2026-10-29" or "2026-10-29T09:05" → local Date
  const [y, m, d, h = 0, mi = 0] = s.split(/[-T:]/).map(Number);
  return new Date(y, m - 1, d, h, mi);
}
const ymd = (n) => `${n.getFullYear()}-${String(n.getMonth() + 1).padStart(2, '0')}-${String(n.getDate()).padStart(2, '0')}`;
const todayStr = () => ymd(new Date());
const fmtDay = (s) => parseDate(s).toLocaleDateString('en-GB', { weekday: 'short', day: 'numeric', month: 'short' }).replace(',', '');
const fmtStamp = (s) => { const d = parseDate(s); return `${d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' })}, ${d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })}`; };
const fmtDate = (s) => parseDate(s).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' });

// isoWeek is the ISO 8601 week number (weeks start on Monday), as used in Norway.
function isoWeek(date) {
  const t = new Date(Date.UTC(date.getFullYear(), date.getMonth(), date.getDate()));
  t.setUTCDate(t.getUTCDate() + 4 - (t.getUTCDay() || 7));
  return Math.ceil(((t - Date.UTC(t.getUTCFullYear(), 0, 1)) / 864e5 + 1) / 7);
}

// due describes a deadline relative to today: { cls: over|today|norm, text }.
function due(deadline) {
  const today = todayStr();
  if (deadline < today) {
    const days = Math.round((parseDate(today) - parseDate(deadline)) / 864e5);
    return { cls: 'over', text: `${fmtDay(deadline)} · ${plural(days, 'day')} late` };
  }
  if (deadline === today) return { cls: 'today', text: 'Today' };
  return { cls: 'norm', text: fmtDay(deadline) };
}

function toast(msg) {
  toastEl.textContent = msg;
  toastEl.hidden = false;
  clearTimeout(toast.t);
  toast.t = setTimeout(() => { toastEl.hidden = true; }, 5000);
}

const errText = (e) => (e && e.message) || String(e);

// act runs a change, reports any error, and redraws.
async function act(fn) {
  try { await fn(); } catch (e) { toast(errText(e)); }
  await render();
}

// withConfirm runs a status change; if it would complete a task with open
// subtasks, it asks first and repeats the call with completeSubtasks = true.
async function withConfirm(call) {
  const res = await call(false);
  if (!res || !res.needsConfirm) return;
  const n = res.openSubtasks;
  const ok = await confirmDialog(`${plural(n, 'subtask')} still open`,
    `Complete ${n === 1 ? 'it' : 'them'} too and mark this task as done?`, 'Complete all');
  if (ok) await call(true);
}

// saveSettings stores changed settings; Go returns the stored version.
async function saveSettings(change) {
  const next = structuredClone(state.settings);
  change(next);
  state.settings = await api.SaveSettings(next);
  applyTheme();
}

// ---------- Theme ----------

const media = window.matchMedia('(prefers-color-scheme: dark)');
const isDark = () => (prefs().theme ? prefs().theme === 'dark' : media.matches);

function applyTheme() {
  const dark = isDark();
  appEl.classList.toggle('dark', dark);
  document.body.classList.toggle('dark', dark);
  const btn = document.getElementById('theme-btn');
  const label = dark ? 'Switch to light mode' : 'Switch to dark mode';
  btn.innerHTML = dark ? icon.sun : icon.moon;
  btn.setAttribute('aria-label', label);
  btn.title = label;
}
media.addEventListener('change', applyTheme);

// ---------- Routing and drawing ----------

function parseRoute() {
  const parts = location.hash.replace(/^#\/?/, '').split('/').filter(Boolean).map(decodeURIComponent);
  if (parts[0] === 'projects') return { view: 'projects' };
  if (parts[0] === 'settings') return { view: 'settings' };
  if (parts[0] === 'p' && parts[1]) {
    if (parts[2] === 't' && Number(parts[3]) > 0) return { view: 'task', slug: parts[1], id: Number(parts[3]) };
    return { view: 'board', slug: parts[1] };
  }
  return { view: 'dashboard' };
}

const hasLibrary = () => !!prefs().libraryPath && !prefs().libraryError;

async function render() {
  const route = parseRoute();
  const key = location.hash;
  const sameView = key === state.routeKey;
  if (!sameView) {
    state.editingNote = state.editTitle = state.editDesc = state.colsDraft = null;
    state.routeKey = key;
  }
  const hasLib = hasLibrary();
  document.getElementById('mainnav').hidden = !hasLib;
  document.getElementById('new-task-btn').hidden = !hasLib;
  document.getElementById('quick-btn').hidden = !hasLib;
  document.querySelectorAll('[data-nav]').forEach((a) => {
    const on = a.dataset.nav === route.view || (a.dataset.nav === 'projects' && (route.view === 'board' || route.view === 'task'));
    if (on) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current');
  });
  renderQuick();
  if (!hasLib && route.view !== 'settings') { view.innerHTML = welcomeHTML(); return; }

  const scrollY = window.scrollY;
  try {
    let html;
    if (route.view === 'dashboard') {
      const [d, problems] = await Promise.all([api.Dashboard(), api.Problems()]);
      state.data = d;
      html = dashboardHTML(d, problems || []);
    } else if (route.view === 'projects') {
      const [projects, problems] = await Promise.all([api.Projects(), api.Problems()]);
      state.data = projects;
      html = projectsHTML(projects || [], problems || []);
    } else if (route.view === 'settings') {
      const problems = hasLib ? await api.Problems() : [];
      if (!state.colsDraft) state.colsDraft = prefs().projects.defaultColumns.map((c) => ({ name: c.name, type: c.type }));
      html = settingsHTML(problems || []);
    } else if (route.view === 'board') {
      const b = await api.Board(route.slug);
      state.data = b;
      html = boardHTML(b);
    } else {
      const d = await api.Task(route.slug, route.id);
      state.data = d;
      html = taskHTML(d);
    }
    view.innerHTML = html;
  } catch (e) {
    state.data = null;
    const msg = errText(e);
    const title = /not found/i.test(msg) ? 'Not found' : 'Something went wrong';
    view.innerHTML = `<div class="page"><div class="card card-pad"><h1 style="font-size: 20px">${title}</h1><p class="muted" style="margin: 8px 0 16px">${esc(msg)}</p><a class="btn btn-ghost" href="#/">Back to dashboard</a></div></div>`;
  }
  window.scrollTo(0, sameView ? scrollY : 0);
  const auto = view.querySelector('[data-autofocus]');
  if (auto) { auto.focus(); auto.select?.(); }
}

function welcomeHTML() {
  const s = prefs();
  if (s.libraryError) {
    return `<div class="card welcome">
      <h1>Can't open your library</h1>
      <p>${esc(s.libraryError.charAt(0).toUpperCase() + s.libraryError.slice(1))}.</p>
      <p>If it is on a drive that isn't connected, connect it and try again. Otherwise choose where the folder is now.</p>
      <div style="display: flex; flex-wrap: wrap; gap: 8px">
        <button type="button" class="btn btn-primary" data-action="retryLibrary">Try again</button>
        <button type="button" class="btn btn-ghost" data-action="chooseLibrary">Choose folder</button></div>
    </div>`;
  }
  return `<div class="card welcome">
    <h1>Welcome to Tasks</h1>
    <p>Choose the folder where your projects will be kept. Each project becomes its own folder of plain text files, so you can copy, zip, move or back it up like any other folder.</p>
    <p>Pick an empty folder to start fresh, or a folder that already holds your projects.</p>
    <div><button type="button" class="btn btn-primary" data-action="chooseLibrary">Choose folder</button></div>
  </div>`;
}

function problemsBanner(problems) {
  if (!problems.length) return '';
  return `<div class="banner" role="status"><span><strong>${plural(problems.length, 'file')}</strong> could not be read as expected.</span><a href="#/settings">See details</a></div>`;
}

const dotHTML = (c, size = 10) => `<span class="dot" aria-hidden="true" style="width: ${size}px; height: ${size}px; background: ${color(c)}"></span>`;
const priChip = (p) => `<span class="chip pri-${esc(p)}">${PRIORITY[p] || esc(p)}</span>`;
const newProjectCard = () => `<button type="button" class="newcard" data-action="newProject">${icon.plus(16)} New project</button>`;

// ---------- Dashboard ----------

function dashboardHTML(d, problems) {
  const ds = prefs().dashboard;
  const now = new Date();
  const hour = now.getHours();
  const greeting = hour < 12 ? 'Good morning' : hour < 18 ? 'Good afternoon' : 'Good evening';
  const week = isoWeek(now);
  const dateLine = now.toLocaleDateString('en-GB', { weekday: 'long', day: 'numeric', month: 'long' }) + (ds.showWeekNumbers ? ` · Week ${week}` : '');
  const summary = [
    d.overdue ? `<strong>${d.overdue} overdue</strong>` : '0 overdue',
    `${d.dueToday} due today`,
    `${plural(d.activeCount, 'active task')} across ${plural(d.activeProjects, 'project')}`,
  ].join(' · ');
  const projects = d.projects || [];
  const groups = (d.groups || []).filter((g) => g.tasks && g.tasks.length);

  if (!projects.length) {
    return `<div class="page">${problemsBanner(problems)}
      <div class="card welcome" style="margin-top: 24px">
        <h1>No projects yet</h1>
        <p>Projects hold your tasks, a board with columns, milestones and a notes log. Create your first one to get started.</p>
        <div><button type="button" class="btn btn-primary" data-action="newProject">${icon.plus(16)} New project</button></div>
      </div></div>`;
  }

  const groupLabel = (g) => (g.key === 'week' && ds.showWeekNumbers ? `${g.label} · ${week}` : g.label);
  const important = groups.length ? groups.map((g) => `
    <div style="margin-top: 22px">
      <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 10px">
        <span class="grp g-${g.key}">${esc(groupLabel(g))}</span><span class="count">${g.tasks.length}</span>
      </div>
      <ul class="tasklist">${g.tasks.map(importantRowHTML).join('')}</ul>
    </div>`).join('') : `<p class="empty" style="margin-top: 22px">Nothing open with a deadline.</p>`;

  const active = (d.active || []).length ? d.active.map((p) => `
    <div style="margin-top: 22px">
      <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 8px">
        ${dotHTML(p.color)}<h3 style="font-size: 14px; font-weight: 700; flex: 1">${esc(p.name)}</h3>
        <a href="${boardHref(p.slug)}" style="font-size: 13px; font-weight: 600; text-decoration: none; padding: 6px 0">Open board</a>
      </div>
      <ul class="activelist">${p.tasks.map((t) => `<li class="clickable"><a class="stretch" href="${taskHref(p.slug, t.id)}">${esc(t.title)}</a><span class="chip st-${esc(t.tone)}">${esc(t.statusName)}</span></li>`).join('')}</ul>
    </div>`).join('') : `<p class="empty" style="margin-top: 22px">Nothing in progress right now.</p>`;

  const importantSection = ds.showImportant ? `<section class="card card-pad" aria-labelledby="imp-h" style="flex: 3 1 520px; min-width: 0">
        <div class="section-head"><h2 id="imp-h">Important tasks</h2><span class="muted" style="font-size: 13px">Sorted by deadline, then priority</span></div>
        ${important}
      </section>` : '';
  const activeSection = ds.showActive ? `<section class="card card-pad" aria-labelledby="act-h" style="flex: 2 1 340px; min-width: 0">
        <div class="section-head"><h2 id="act-h">Active tasks</h2><span class="muted" style="font-size: 13px">Tasks in active columns</span></div>
        ${active}
      </section>` : '';

  return `<div class="page">
    ${problemsBanner(problems)}
    <div class="greeting">
      <p class="date">${esc(dateLine)}</p>
      <h1>${greeting}</h1>
      <p>${summary}</p>
    </div>
    ${importantSection || activeSection ? `<div class="row">${importantSection}${activeSection}</div>` : ''}
    ${ds.showProjects ? `<section aria-labelledby="proj-h">
      <div class="section-head" style="margin-bottom: 12px"><h2 id="proj-h">Projects</h2><a href="#/projects" style="font-size: 14px; font-weight: 600; text-decoration: none">View all</a></div>
      <div class="projgrid">${projects.map(projectCardHTML).join('')}${newProjectCard()}</div>
    </section>` : ''}
  </div>`;
}

function importantRowHTML(t) {
  const dd = due(t.deadline);
  const slug = esc(t.project.slug);
  if (t.kind === 'milestone') {
    return `<li class="taskrow clickable">
      <input type="checkbox" class="check" data-change="milestoneDone" data-slug="${slug}" data-id="${t.id}" aria-label="Mark milestone ${esc(t.title)} as done">
      <div style="flex: 1 1 220px; min-width: 0">
        <a class="title stretch" href="${boardHref(t.project.slug)}">${esc(t.title)}</a>
        <div class="meta">${dotHTML(t.project.color)}<span>${esc(t.project.name)}</span>
          ${t.tasksTotal ? `<span aria-hidden="true">·</span><span>${t.tasksDone} of ${plural(t.tasksTotal, 'task')} done</span>` : ''}</div>
      </div>
      <span class="due-text ${dd.cls}">${icon.cal()}${esc(dd.text)}</span>
      <span class="chip chip-ms">${icon.diamond()}Milestone</span>
    </li>`;
  }
  return `<li class="taskrow clickable">
    <input type="checkbox" class="check" data-change="done" data-slug="${slug}" data-id="${t.id}" aria-label="Mark ${esc(t.title)} as done">
    <div style="flex: 1 1 220px; min-width: 0">
      <a class="title stretch" href="${taskHref(t.project.slug, t.id)}">${esc(t.title)}</a>
      <div class="meta">${dotHTML(t.project.color)}<span>${esc(t.project.name)}</span>
        ${t.parentTitle ? `<span aria-hidden="true">·</span><span>in ${esc(t.parentTitle)}</span>` : ''}
        <span aria-hidden="true">·</span><span>${esc(t.statusName)}</span></div>
    </div>
    <span class="due-text ${dd.cls}">${icon.cal()}${esc(dd.text)}</span>
    ${priChip(t.priority)}
  </li>`;
}

function projectCardHTML(p) {
  return `<a href="${boardHref(p.slug)}" class="card projcard">
    <div style="display: flex; align-items: center; gap: 8px">${dotHTML(p.color)}
      <span style="font-size: 16px; font-weight: 700; flex: 1">${esc(p.name)}</span>
      <span class="muted" style="font-size: 13px">${p.pct}%</span></div>
    <div class="bar" style="margin: 14px 0 12px"><div style="width: ${p.pct}%"></div></div>
    <div class="stats"><span>To do <strong>${p.todo}</strong></span><span>Active <strong>${p.active}</strong></span><span>Done <strong>${p.done}</strong></span></div>
  </a>`;
}

// ---------- Projects ----------

function projectsHTML(projects, problems) {
  const open = projects.filter((p) => !p.archived);
  const archived = projects.filter((p) => p.archived);
  return `<div class="page">
    ${problemsBanner(problems)}
    <div class="section-head"><h1 style="font-size: 28px; font-weight: 700; letter-spacing: -0.02em">Projects</h1>
      <button type="button" class="btn btn-primary" data-action="newProject">${icon.plus(16)} New project</button></div>
    <div class="projgrid">${open.map(projectCardHTML).join('')}${newProjectCard()}</div>
    ${archived.length ? `<section aria-labelledby="arch-h"><h2 id="arch-h" style="font-size: 18px; font-weight: 700; margin-bottom: 12px">Archived</h2>
      <div class="projgrid">${archived.map(projectCardHTML).join('')}</div></section>` : ''}
  </div>`;
}

// ---------- Settings ----------

function checkRow(key, label, hint) {
  const [group, name] = key.split('.');
  const id = `set-${group}-${name}`;
  return `<div class="set-row"><input type="checkbox" class="check" id="${id}" data-change="pref" data-key="${key}" ${prefs()[group][name] ? 'checked' : ''}>
    <label for="${id}"><span>${esc(label)}</span>${hint ? `<span class="muted">${esc(hint)}</span>` : ''}</label></div>`;
}

function settingsHTML(problems) {
  const s = prefs();
  const cols = state.colsDraft;
  return `<div class="page settings">
    <h1 style="font-size: 28px; font-weight: 700; letter-spacing: -0.02em">Settings</h1>
    <p class="muted" style="margin-top: -12px">Saved on this computer. Your projects and tasks are not affected.</p>

    <section class="card card-pad" aria-labelledby="set-gen">
      <h2 id="set-gen">General</h2>
      <div class="field" style="max-width: 320px"><label for="set-theme" class="lbl">Theme</label>
        <select id="set-theme" class="select" data-change="pref" data-key="theme">
          ${[['', 'Follow Windows'], ['light', 'Light'], ['dark', 'Dark']].map(([v, l]) => `<option value="${v}" ${s.theme === v ? 'selected' : ''}>${l}</option>`).join('')}
        </select></div>
      <div class="field" style="margin-top: 18px"><span class="lbl">Library folder</span>
        <p style="font-size: 14px; color: var(--text2); word-break: break-all">${esc(s.libraryPath || 'Not chosen yet')}</p>
        <div style="display: flex; flex-wrap: wrap; gap: 8px; margin-top: 4px">
          ${hasLibrary() ? '<button type="button" class="btn btn-ghost" data-action="showFolder" data-slug="">Show in Explorer</button>' : ''}
          <button type="button" class="btn btn-ghost" data-action="chooseLibrary">Change folder</button></div></div>
    </section>

    <section class="card card-pad" aria-labelledby="set-dash">
      <h2 id="set-dash">Dashboard</h2>
      ${checkRow('dashboard.showImportant', 'Show Important tasks')}
      ${checkRow('dashboard.showActive', 'Show Active tasks')}
      ${checkRow('dashboard.showProjects', 'Show Projects')}
      ${checkRow('dashboard.showMilestones', 'Show milestones in Important tasks', 'Open milestones are listed with the tasks, by date.')}
      ${checkRow('dashboard.includeSubtasks', 'Show subtasks with a deadline in Important tasks')}
      ${checkRow('dashboard.showWeekNumbers', 'Show week numbers')}
    </section>

    <section class="card card-pad" aria-labelledby="set-proj">
      <h2 id="set-proj">Projects</h2>
      ${checkRow('projects.showTimeline', 'Show the milestone timeline on project boards', 'Only appears in projects that have milestones.')}
      <h3 style="font-size: 14px; font-weight: 700; margin-top: 20px">Columns for new projects</h3>
      <p class="muted" style="font-size: 13px; margin: 4px 0 12px">Existing projects keep their columns; change those in the project's project.yaml file. A board needs at least one Not started and one Done column.</p>
      <ol class="col-editor">${cols.map((c, i) => `<li>
        <label class="sr" for="col-name-${i}">Column ${i + 1} name</label>
        <input id="col-name-${i}" class="input" value="${esc(c.name)}" data-coldraft="${i}" data-field="name">
        <label class="sr" for="col-type-${i}">Column ${i + 1} type</label>
        <select id="col-type-${i}" class="select" data-coldraft="${i}" data-field="type">${Object.entries(COLUMN_TYPES).map(([v, l]) => `<option value="${v}" ${c.type === v ? 'selected' : ''}>${l}</option>`).join('')}</select>
        <button type="button" class="icon-btn" data-action="colMove" data-i="${i}" data-dir="-1" aria-label="Move ${esc(c.name)} up" ${i === 0 ? 'disabled' : ''}>${icon.up}</button>
        <button type="button" class="icon-btn" data-action="colMove" data-i="${i}" data-dir="1" aria-label="Move ${esc(c.name)} down" ${i === cols.length - 1 ? 'disabled' : ''}>${icon.down}</button>
        <button type="button" class="icon-btn" data-action="colRemove" data-i="${i}" aria-label="Remove ${esc(c.name)}" ${cols.length <= 2 ? 'disabled' : ''}>${icon.close}</button>
      </li>`).join('')}</ol>
      <div style="display: flex; flex-wrap: wrap; gap: 8px; margin-top: 12px">
        <button type="button" class="btn btn-ghost" data-action="colAdd">${icon.plus()} Add column</button>
        <button type="button" class="btn btn-ghost" data-action="colReset">Reset to default</button>
        <button type="button" class="btn btn-primary" data-action="colSave" style="margin-left: auto">Save columns</button></div>
    </section>

    <section class="card card-pad" aria-labelledby="set-task">
      <h2 id="set-task">Tasks</h2>
      <div class="field" style="max-width: 320px"><label for="set-pri" class="lbl">Default priority for new tasks</label>
        <select id="set-pri" class="select" data-change="pref" data-key="tasks.defaultPriority">
          ${Object.entries(PRIORITY).map(([v, l]) => `<option value="${v}" ${s.tasks.defaultPriority === v ? 'selected' : ''}>${l}</option>`).join('')}
        </select></div>
      ${checkRow('tasks.showMilestoneOnCards', 'Show the milestone on board cards')}
    </section>

    ${problems.length ? `<section class="card card-pad" aria-labelledby="set-prob">
      <h2 id="set-prob" style="color: var(--warn-text)">Files with problems</h2>
      <p class="muted" style="font-size: 13px; margin-top: 4px">The app does not change these files. Fix them in a text editor and they are picked up automatically.</p>
      <ul class="problems">${problems.map((p) => `<li><code>${esc(p.path)}</code><br>${esc(p.message)}</li>`).join('')}</ul>
    </section>` : ''}
  </div>`;
}

// ---------- Board ----------

function boardHTML(b) {
  const p = b.project;
  const firstOpen = (b.columns.find((c) => c.type === 'not_started') || b.columns[0] || {}).id;
  const collapsed = new Set(((prefs().collapsed || {})[p.slug]) || []);
  const sub = [p.description ? esc(p.description.split('\n')[0]) : '', p.deadline ? `Deadline ${esc(fmtDay(p.deadline))}` : '']
    .filter(Boolean).join(' · ');
  return `<div class="page wide">
    <nav aria-label="Breadcrumb" class="breadcrumb"><a href="#/">Dashboard</a><span aria-hidden="true">/</span><span class="here">${esc(p.name)}</span></nav>
    <div style="display: flex; flex-wrap: wrap; align-items: flex-end; justify-content: space-between; gap: 16px">
      <div style="min-width: 0">
        <div style="display: flex; align-items: center; gap: 10px">${dotHTML(p.color, 14)}
          <h1 style="font-size: 28px; font-weight: 700; letter-spacing: -0.02em">${esc(p.name)}</h1>
          ${p.archived ? '<span class="chip st-todo">Archived</span>' : ''}</div>
        ${sub ? `<p style="margin-top: 6px; font-size: 15px; color: var(--text2)">${sub}</p>` : ''}
      </div>
      <div style="display: flex; flex-wrap: wrap; align-items: center; gap: 12px">
        <div style="min-width: 200px">
          <div style="display: flex; justify-content: space-between; font-size: 13px; color: var(--muted); margin-bottom: 6px"><span>${p.done} of ${p.total} done</span><span>${p.pct}%</span></div>
          <div class="bar" style="background: var(--border)"><div style="width: ${p.pct}%"></div></div>
        </div>
        <button type="button" class="btn btn-ghost" data-action="newMilestone">${icon.diamond(14)} Milestone</button>
        <button type="button" class="btn btn-ghost" data-action="editProject">Edit project</button>
        <button type="button" class="btn btn-ghost" data-action="showFolder" data-slug="${esc(p.slug)}">Show folder</button>
      </div>
    </div>
    ${timelineHTML(b)}
    <div class="board">
      ${b.columns.map((col) => {
        const cards = col.cards || [];
        if (collapsed.has(col.id)) {
          return `<section class="column collapsed" data-status="${esc(col.id)}" aria-label="${esc(col.name)}, collapsed">
            <button type="button" class="col-toggle" data-action="toggleColumn" data-col="${esc(col.id)}" aria-expanded="false" title="Show ${esc(col.name)}">
              ${icon.chevron(false)}<span class="col-vname">${esc(col.name)}</span><span class="count">${cards.length}</span></button>
          </section>`;
        }
        return `<section class="column" data-status="${esc(col.id)}" aria-label="${esc(col.name)}">
          <div class="column-head"><button type="button" class="col-toggle" data-action="toggleColumn" data-col="${esc(col.id)}" aria-expanded="true" title="Collapse ${esc(col.name)}">
            ${icon.chevron(true)}<h2>${esc(col.name)}</h2></button><span class="count">${cards.length}</span></div>
          ${cards.map((c) => cardHTML(p.slug, c)).join('')}
          ${col.id === firstOpen ? `<button type="button" class="addcard" data-action="newTask" data-slug="${esc(p.slug)}" data-status="${esc(col.id)}">${icon.plus()} Add task</button>` : ''}
        </section>`;
      }).join('')}
    </div>
    ${notesHTML({ key: `p:${p.slug}`, slug: p.slug, task: 0, notes: b.notes, title: 'Project notes', hint: 'Notes for the whole project, newest first. Task notes live on each task.' })}
  </div>`;
}

// timelineHTML draws the project's milestones on a line from the project's
// start to its deadline (or last milestone). Hidden when there are none.
function timelineHTML(b) {
  const ms = b.milestones || [];
  if (!ms.length || !prefs().projects.showTimeline) return '';
  const today = todayStr();
  const dates = [b.start, today, ...ms.map((m) => m.date)];
  if (b.project.deadline) dates.push(b.project.deadline);
  let lo = Math.min(...dates.map((d) => parseDate(d).getTime()));
  let hi = Math.max(...dates.map((d) => parseDate(d).getTime()));
  const pad = Math.max((hi - lo) * 0.03, 864e5);
  lo -= pad; hi += pad;
  const pos = (d) => (((parseDate(d).getTime() - lo) / (hi - lo)) * 100).toFixed(2);
  const msState = (m) => (m.done ? 'done' : m.date < today ? 'over' : m.date === today ? 'today' : 'open');
  const stateText = { done: 'Done', over: 'Overdue', today: 'Today', open: '' };
  return `<section class="card card-pad timeline" aria-labelledby="ms-h">
    <div class="section-head"><h2 id="ms-h" style="font-size: 16px">Milestones</h2>
      <span class="muted" style="font-size: 13px">${fmtDay(b.start)} – ${fmtDay([b.project.deadline, ms[ms.length - 1].date].filter(Boolean).sort().pop())}</span></div>
    <div class="tl-track" aria-hidden="true">
      <div class="tl-line"></div>
      <div class="tl-today" style="left: ${pos(today)}%"><span>Today</span></div>
      ${b.project.deadline ? `<div class="tl-end" style="left: ${pos(b.project.deadline)}%"><span>Deadline</span></div>` : ''}
      ${ms.map((m, i) => `<span class="tl-marker ${msState(m)}" style="left: ${pos(m.date)}%" title="${esc(m.title)} · ${esc(fmtDay(m.date))}"><span>${i + 1}</span></span>`).join('')}
    </div>
    <ol class="tl-list">${ms.map((m, i) => {
      const st = msState(m);
      return `<li><button type="button" class="tl-item ${st}" data-action="editMilestone" data-id="${m.id}">
        <span class="tl-num">${i + 1}</span>
        <span class="tl-body"><span class="tl-title">${esc(m.title)}</span>
          <span class="tl-meta">${esc(fmtDay(m.date))}${stateText[st] ? ` · ${stateText[st]}` : ''}${m.tasksTotal ? ` · ${m.tasksDone}/${m.tasksTotal} tasks` : ''}</span></span>
      </button></li>`;
    }).join('')}</ol>
  </section>`;
}

function cardHTML(slug, c) {
  if (c.done) {
    return `<article class="card bcard clickable" draggable="true" data-id="${c.id}"><div class="donecard">${icon.check}<div>
      <a class="title stretch" draggable="false" href="${taskHref(slug, c.id)}">${esc(c.title)}</a>
      ${c.completed ? `<div class="muted" style="font-size: 12px; margin-top: 4px">Completed ${esc(fmtDay(c.completed.slice(0, 10)))}</div>` : ''}
    </div></div></article>`;
  }
  const key = `${slug}:${c.id}`;
  const open = state.expanded.has(key);
  const pct = c.subTotal ? Math.round((c.subDone / c.subTotal) * 100) : 0;
  const dd = c.deadline ? due(c.deadline) : null;
  const ms = c.milestone && prefs().tasks.showMilestoneOnCards ? `<span class="chip chip-ms">${icon.diamond()}${esc(c.milestone.title)}</span>` : '';
  return `<article class="card bcard clickable" draggable="true" data-id="${c.id}">
    <a class="title stretch" draggable="false" href="${taskHref(slug, c.id)}">${esc(c.title)}</a>
    ${c.waitingFor ? `<div class="waiting">${icon.clock}Waiting for: ${esc(c.waitingFor)}</div>` : ''}
    ${c.subTotal ? `<div>
      <div class="subprog-head"><span>Subtasks ${c.subDone}/${c.subTotal}</span>
        <button type="button" class="btn-link" data-action="toggleSubtasks" data-key="${esc(key)}" aria-expanded="${open}">${open ? 'Hide' : 'Show'}</button></div>
      <div class="bar thin"><div style="width: ${pct}%"></div></div>
    </div>` : ''}
    ${open ? `<ul class="sublist">${(c.subtasks || []).map((s) => `<li style="padding-left: ${s.depth * 24}px">
      <input type="checkbox" class="check" style="width: 16px; height: 16px" data-change="done" data-slug="${esc(slug)}" data-id="${s.id}" ${s.done ? 'checked' : ''} aria-label="${esc(s.title)} done">
      <a href="${taskHref(slug, s.id)}" draggable="false" style="color: ${s.done ? 'var(--muted)' : 'var(--text)'}; text-decoration: none">${esc(s.title)}</a></li>`).join('')}</ul>` : ''}
    <div class="chips">
      ${dd ? `<span class="chip due-${dd.cls}">${icon.cal(12)}${esc(dd.cls === 'over' ? fmtDay(c.deadline) : dd.text)}</span>` : ''}
      ${priChip(c.priority)}${ms}
    </div>
  </article>`;
}

// ---------- Notes (projects and tasks) ----------

function notesHTML({ key, slug, task, notes, title, hint }) {
  notes = notes || [];
  const draft = state.drafts[key] || '';
  const id = key.replace(/[^a-z0-9]/gi, '-');
  return `<section class="card card-pad" aria-labelledby="${id}-h" ${task ? '' : 'style="max-width: 860px"'}>
    <div style="display: flex; align-items: baseline; gap: 8px"><h2 id="${id}-h" style="font-size: ${task ? 16 : 18}px; font-weight: 700">${esc(title)}</h2><span class="count">${notes.length}</span></div>
    <p class="muted" style="margin: 4px 0 16px; font-size: 13px">${esc(hint)}</p>
    <form class="field" style="gap: 10px" data-submit="addNote" data-key="${esc(key)}" data-slug="${esc(slug)}" data-task="${task}">
      <label for="${id}-new" class="sr">New note</label>
      <textarea id="${id}-new" class="ta" placeholder="Write a note…" data-draft="${esc(key)}">${esc(draft)}</textarea>
      <div class="actions"><button type="submit" class="btn btn-primary" data-for="${esc(key)}" ${draft.trim() ? '' : 'disabled'}>Add note</button></div>
    </form>
    <ul class="notes">${notes.map((n) => {
      const ed = state.editingNote;
      if (ed && ed.key === key && ed.id === n.id) {
        return `<li class="note"><form data-submit="saveNote" data-slug="${esc(slug)}" data-task="${task}" data-note="${n.id}">
          <label for="${id}-edit" class="sr">Edit note</label>
          <textarea id="${id}-edit" class="ta" data-edit="note" data-autofocus>${esc(ed.text)}</textarea>
          <div class="actions" style="margin-top: 10px"><button type="button" class="btn btn-ghost" data-action="cancelNote">Cancel</button><button type="submit" class="btn btn-primary">Save</button></div>
        </form></li>`;
      }
      return `<li class="note"><div class="note-text">${esc(n.text)}</div>
        <div class="note-meta"><span>Added ${esc(fmtStamp(n.created))}</span>${n.edited ? `<span>· Edited ${esc(fmtStamp(n.edited))}</span>` : ''}
          <button type="button" class="btn-link" style="margin-left: auto" data-action="editNote" data-key="${esc(key)}" data-note="${n.id}">Edit</button></div></li>`;
    }).join('')}</ul>
  </section>`;
}

// ---------- Task detail ----------

function taskHTML(d) {
  const t = d.task;
  const slug = d.project.slug;
  const dd = t.deadline && !t.done ? due(t.deadline) : null;
  const dueChip = dd ? `<span class="chip due-${dd.cls}">${icon.cal(12)}${dd.cls === 'over' ? `Overdue · ${esc(fmtDay(t.deadline))}` : dd.cls === 'today' ? 'Due today' : `Due ${esc(fmtDay(t.deadline))}`}</span>` : '';
  const subs = d.subtasks || [];
  const milestones = d.milestones || [];
  const linked = milestones.find((m) => m.id === t.milestone);
  const pct = d.subTotal ? Math.round((d.subDone / d.subTotal) * 100) : 0;
  const deadlineText = t.deadline ? `${dd && dd.cls === 'today' ? 'Today · ' : ''}${fmtDay(t.deadline)}` : '';

  const titleBlock = state.editTitle !== null
    ? `<form data-submit="saveTitle" style="display: flex; gap: 8px; margin-bottom: 10px; flex-wrap: wrap">
        <label for="t-title" class="sr">Title</label>
        <input id="t-title" class="input" style="flex: 1 1 280px; font-size: 18px; font-weight: 600" data-edit="title" value="${esc(state.editTitle)}" data-autofocus>
        <button type="button" class="btn btn-ghost" data-action="cancelTitle">Cancel</button><button type="submit" class="btn btn-primary">Save</button></form>`
    : `<div style="display: flex; align-items: flex-start; gap: 8px"><h1 class="task-title" style="flex: 1">${esc(t.title)}</h1>
        <button type="button" class="btn-link" data-action="editTitle">Rename</button></div>`;

  const descBlock = state.editDesc !== null
    ? `<form data-submit="saveDesc"><label for="t-desc" class="sr">Description</label>
        <textarea id="t-desc" class="ta" style="min-height: 140px" data-edit="desc" data-autofocus>${esc(state.editDesc)}</textarea>
        <div class="actions" style="margin-top: 10px"><button type="button" class="btn btn-ghost" data-action="cancelDesc">Cancel</button><button type="submit" class="btn btn-primary">Save</button></div></form>`
    : (t.description ? `<p class="desc">${esc(t.description)}</p>` : `<p class="muted">No description yet.</p>`);

  return `<div class="page">
    <nav aria-label="Breadcrumb" class="breadcrumb"><a href="#/">Dashboard</a><span aria-hidden="true">/</span>
      <a href="${boardHref(slug)}">${esc(d.project.name)}</a>
      ${(d.ancestors || []).map((a) => `<span aria-hidden="true">/</span><a href="${taskHref(slug, a.id)}">${esc(a.title)}</a>`).join('')}
      <span aria-hidden="true">/</span><span class="here">${esc(t.title)}</span></nav>
    <div class="row">
      <div class="task-main">
        <div>${titleBlock}
          <div class="chips" style="gap: 8px"><span class="chip st-${esc(t.tone)}">${esc(t.statusName)}</span>${priChip(t.priority)}${dueChip}
            ${linked ? `<span class="chip chip-ms">${icon.diamond()}${esc(linked.title)}</span>` : ''}</div></div>
        <section class="card card-pad" aria-labelledby="desc-h">
          <div style="display: flex; align-items: center; margin-bottom: 10px"><h2 id="desc-h" style="font-size: 16px; font-weight: 700; flex: 1">Description</h2>
            ${state.editDesc === null ? '<button type="button" class="btn-link" data-action="editDesc">Edit</button>' : ''}</div>
          ${descBlock}
        </section>
        <section class="card card-pad" aria-labelledby="sub-h">
          <div style="display: flex; align-items: center; gap: 10px; margin-bottom: 8px"><h2 id="sub-h" style="font-size: 16px; font-weight: 700; flex: 1">Subtasks</h2>
            ${d.subTotal ? `<span class="muted" style="font-size: 13px">${d.subDone} of ${d.subTotal} done</span>` : ''}</div>
          ${d.subTotal ? `<div class="bar thin" style="margin-bottom: 8px"><div style="width: ${pct}%"></div></div>` : '<p class="muted" style="font-size: 13px">Break this task into smaller steps.</p>'}
          <ul class="subrows">${subs.map((s) => `<li class="${s.done ? 'is-done' : ''}" style="padding-left: ${s.depth * 28}px">
            <input type="checkbox" class="check" data-change="done" data-slug="${esc(slug)}" data-id="${s.id}" ${s.done ? 'checked' : ''} aria-label="${esc(s.title)} done">
            <a href="${taskHref(slug, s.id)}">${esc(s.title)}</a></li>`).join('')}</ul>
          <form class="inline-form" data-submit="addSubtask">
            <label for="new-sub" class="sr">New subtask</label>
            <input id="new-sub" class="input" placeholder="Add a subtask…" data-edit="subtask">
            <button type="submit" class="btn btn-ghost">${icon.plus()} Add</button>
          </form>
        </section>
        ${notesHTML({ key: `t:${slug}:${t.id}`, slug, task: t.id, notes: d.notes, title: 'Notes', hint: 'Work notes for this task, newest first.' })}
      </div>
      <aside class="card task-side" aria-label="Task details">
        <div class="field"><label for="t-status" class="lbl">Status</label>
          <select id="t-status" class="select" data-change="status">${d.columns.map((c) => `<option value="${esc(c.id)}" ${c.id === t.status ? 'selected' : ''}>${esc(c.name)}</option>`).join('')}</select></div>
        <div class="field"><label for="t-pri" class="lbl">Priority</label>
          <select id="t-pri" class="select" data-change="priority">${Object.entries(PRIORITY).map(([v, l]) => `<option value="${v}" ${v === t.priority ? 'selected' : ''}>${l}</option>`).join('')}</select></div>
        <div class="field"><label for="t-deadline" class="lbl">Deadline</label>
          ${deadlineText ? `<span style="font-size: 14px; font-weight: 600; color: ${dd && dd.cls !== 'norm' ? `var(--${dd.cls === 'over' ? 'danger' : 'link-strong'})` : 'var(--text)'}">${esc(deadlineText)}</span>` : ''}
          <div style="display: flex; gap: 8px"><input id="t-deadline" type="date" class="input" data-change="deadline" value="${esc(t.deadline || '')}">
            ${t.deadline ? '<button type="button" class="btn btn-ghost" data-action="clearDeadline">Clear</button>' : ''}</div></div>
        ${milestones.length ? `<div class="field"><label for="t-ms" class="lbl">Milestone</label>
          <select id="t-ms" class="select" data-change="milestone"><option value="0">None</option>
            ${milestones.map((m) => `<option value="${m.id}" ${m.id === t.milestone ? 'selected' : ''}>${esc(m.title)}</option>`).join('')}</select></div>` : ''}
        <div class="field"><label for="t-wait" class="lbl">Waiting for</label>
          <input id="t-wait" class="input" data-change="waiting" placeholder="Who or what?" value="${esc(t.waitingFor)}"></div>
        <div class="field"><span class="lbl">Project</span>
          <a href="${boardHref(slug)}" style="font-size: 14px; font-weight: 600; text-decoration: none; display: inline-flex; align-items: center; gap: 8px; min-height: 24px">${dotHTML(d.project.color)}${esc(d.project.name)}</a></div>
        <div class="side-meta"><span>Created ${esc(fmtDate(t.created))}</span><span>Last updated ${esc(fmtStamp(t.updated))}</span>
          ${t.completed ? `<span>Completed ${esc(fmtStamp(t.completed))}</span>` : ''}</div>
        <button type="button" class="btn btn-ghost" style="width: 100%" data-action="markDone" data-done="${!t.done}">${t.done ? 'Reopen task' : 'Mark as done'}</button>
        <button type="button" class="btn btn-danger" style="width: 100%" data-action="deleteTask">Delete task</button>
      </aside>
    </div>
  </div>`;
}

// ---------- Quick list (side bar) ----------

async function renderQuick() {
  const open = hasLibrary() && prefs().quickList;
  quickEl.hidden = !open;
  const btn = document.getElementById('quick-btn');
  btn.setAttribute('aria-pressed', String(open));
  if (!open) return;
  let items = [];
  let error = '';
  try { items = (await api.Inbox()) || []; } catch (e) { error = errText(e); }
  const done = items.filter((i) => i.done).length;
  const hadFocus = document.activeElement && document.activeElement.id === 'quick-new';
  quickEl.innerHTML = `<div class="quick-head"><h2 id="quick-h">Quick list</h2>
      <button type="button" class="icon-btn" data-qaction="close" aria-label="Close quick list">${icon.close}</button></div>
    <p class="muted" style="font-size: 13px">Loose to-dos that don't belong to a project.</p>
    ${error ? `<p class="banner">${esc(error)}</p>` : `
    <form class="inline-form" data-qsubmit="add" style="margin-top: 0">
      <label for="quick-new" class="sr">New item</label>
      <input id="quick-new" class="input" placeholder="Add an item…" autocomplete="off" value="${esc(state.quickDraft)}">
      <button type="submit" class="btn btn-primary">Add</button>
    </form>
    ${items.length ? `<ul class="quick-list">${items.map((it) => `<li class="${it.done ? 'is-done' : ''}">
      <input type="checkbox" class="check" id="qi-${it.id}" data-qchange="done" data-id="${it.id}" ${it.done ? 'checked' : ''}>
      <label for="qi-${it.id}">${esc(it.text)}</label>
      <button type="button" class="icon-btn quick-del" data-qaction="delete" data-id="${it.id}" aria-label="Delete ${esc(it.text)}">${icon.close}</button>
    </li>`).join('')}</ul>` : '<p class="empty">Nothing here yet.</p>'}
    ${done ? `<button type="button" class="btn btn-ghost" data-qaction="clear">Clear done (${done})</button>` : ''}`}`;
  if (hadFocus) document.getElementById('quick-new')?.focus();
}

async function quickAct(fn) {
  try { await fn(); } catch (e) { toast(errText(e)); }
  await renderQuick();
}

quickEl.addEventListener('click', (e) => {
  const el = e.target.closest('[data-qaction]');
  if (!el) return;
  const id = Number(el.dataset.id);
  if (el.dataset.qaction === 'close') actions.toggleQuick();
  if (el.dataset.qaction === 'delete') quickAct(() => api.DeleteInboxItem(id));
  if (el.dataset.qaction === 'clear') quickAct(() => api.ClearInboxDone());
});
quickEl.addEventListener('change', (e) => {
  const el = e.target.closest('[data-qchange]');
  if (el) quickAct(() => api.SetInboxDone(Number(el.dataset.id), el.checked));
});
quickEl.addEventListener('input', (e) => {
  if (e.target.id === 'quick-new') state.quickDraft = e.target.value;
});
quickEl.addEventListener('submit', (e) => {
  e.preventDefault();
  const text = state.quickDraft.trim();
  if (!text) return;
  quickAct(async () => {
    await api.AddInboxItem(text);
    state.quickDraft = '';
    setTimeout(() => document.getElementById('quick-new')?.focus(), 0);
  });
});

// ---------- Dialogs ----------

// showDialog opens the dialog with a form. onSubmit gets the form data; if it
// throws, the message is shown in the dialog and it stays open. Buttons with
// data-dialog-action call extra[name] instead.
function showDialog(html, onSubmit, extra = {}) {
  dlg.innerHTML = `<form class="dialog-body" method="dialog" novalidate>${html}<p class="dlg-error" role="alert" style="color: var(--danger); font-size: 13px" hidden></p></form>`;
  dlg.showModal();
  dlg.querySelector('[data-autofocus]')?.focus();
  return new Promise((resolve) => {
    const form = dlg.querySelector('form');
    const fail = (err) => {
      const p = form.querySelector('.dlg-error');
      p.textContent = errText(err);
      p.hidden = false;
    };
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      try {
        const result = await onSubmit(new FormData(form));
        resolve(result === undefined ? true : result);
        dlg.close();
      } catch (err) { fail(err); }
    });
    form.addEventListener('click', async (e) => {
      const b = e.target.closest('[data-dialog-action]');
      if (!b) return;
      try {
        const result = await extra[b.dataset.dialogAction]();
        if (result !== false) { resolve(true); dlg.close(); }
      } catch (err) { fail(err); }
    });
    dlg.addEventListener('close', () => resolve(null), { once: true });
  });
}

dlg.addEventListener('click', (e) => {
  if (e.target.closest('[data-close]') || e.target === dlg) dlg.close();
});

function confirmDialog(title, text, okLabel, danger = false) {
  return showDialog(`<h2>${esc(title)}</h2><p style="color: var(--text2); line-height: 1.5">${esc(text)}</p>
    <div class="actions"><button type="button" class="btn btn-ghost" data-close>Cancel</button>
    <button type="submit" class="btn ${danger ? 'btn-danger' : 'btn-primary'}" data-autofocus>${esc(okLabel)}</button></div>`, () => true)
    .then((r) => r === true);
}

async function newTaskDialog(slug, status) {
  const projects = ((await api.Projects()) || []).filter((p) => !p.archived);
  if (!projects.length) { toast('Create a project first.'); return projectDialog(null); }
  const route = parseRoute();
  const want = slug || route.slug || prefs().lastProject;
  const chosen = projects.some((p) => p.slug === want) ? want : projects[0].slug;
  const pri = prefs().tasks.defaultPriority;
  const created = await showDialog(`<h2>New task</h2>
    <div class="field"><label for="q-title" class="lbl">Title</label><input id="q-title" name="title" class="input" data-autofocus autocomplete="off"></div>
    <div class="field"><label for="q-project" class="lbl">Project</label>
      <select id="q-project" name="project" class="select" ${slug ? 'disabled' : ''}>${projects.map((p) => `<option value="${esc(p.slug)}" ${p.slug === chosen ? 'selected' : ''}>${esc(p.name)}</option>`).join('')}</select></div>
    <div class="two">
      <div class="field"><label for="q-pri" class="lbl">Priority</label><select id="q-pri" name="priority" class="select">
        ${Object.entries(PRIORITY).map(([v, l]) => `<option value="${v}" ${v === pri ? 'selected' : ''}>${l}</option>`).join('')}</select></div>
      <div class="field"><label for="q-due" class="lbl">Deadline</label><input id="q-due" name="deadline" type="date" class="input"></div>
    </div>
    <div class="actions"><button type="button" class="btn btn-ghost" data-close>Cancel</button><button type="submit" class="btn btn-primary">Add task</button></div>`,
  async (fd) => {
    const target = slug || fd.get('project');
    if (!String(fd.get('title')).trim()) throw new Error('Give the task a title.');
    const id = await api.CreateTask(target, { title: fd.get('title'), priority: fd.get('priority'), deadline: fd.get('deadline'), status: status || '', parent: 0, description: '' });
    state.settings.lastProject = target;
    api.SetLastProject(target);
    return { slug: target, id };
  });
  if (created) {
    toast('Task added.');
    await render();
  }
}

async function projectDialog(p) {
  const current = p ? color(p.color) : PALETTE[0];
  const swatches = PALETTE.map((c, i) => `<label style="background: ${c}"><input type="radio" name="color" value="${c}" ${c.toLowerCase() === current.toLowerCase() ? 'checked' : ''} aria-label="Colour ${i + 1}"><span></span></label>`).join('');
  const custom = PALETTE.some((c) => c.toLowerCase() === current.toLowerCase()) ? '' :
    `<label style="background: ${current}"><input type="radio" name="color" value="${current}" checked aria-label="Current colour"><span></span></label>`;
  const result = await showDialog(`<h2>${p ? 'Edit project' : 'New project'}</h2>
    <div class="field"><label for="p-name" class="lbl">Name</label><input id="p-name" name="name" class="input" value="${esc(p ? p.name : '')}" data-autofocus autocomplete="off"></div>
    <div class="field"><span class="lbl" id="p-color">Colour</span><div class="swatches" role="radiogroup" aria-labelledby="p-color">${swatches}${custom}</div></div>
    <div class="field"><label for="p-desc" class="lbl">Description</label><textarea id="p-desc" name="description" class="ta">${esc(p ? p.description : '')}</textarea></div>
    <div class="field"><label for="p-due" class="lbl">Deadline</label><input id="p-due" name="deadline" type="date" class="input" value="${esc(p && p.deadline ? p.deadline : '')}"></div>
    ${p ? `<label style="display: flex; align-items: center; gap: 10px; min-height: 44px"><input type="checkbox" name="archived" class="check" ${p.archived ? 'checked' : ''}> Archived (hidden from the dashboard)</label>` : ''}
    <div class="actions"><button type="button" class="btn btn-ghost" data-close>Cancel</button><button type="submit" class="btn btn-primary">${p ? 'Save' : 'Create project'}</button></div>`,
  async (fd) => {
    const input = { name: fd.get('name'), color: fd.get('color') || PALETTE[0], description: fd.get('description'), deadline: fd.get('deadline') };
    if (!String(input.name).trim()) throw new Error('Give the project a name.');
    if (p) {
      await api.UpdateProject(p.slug, { ...input, archived: fd.get('archived') === 'on' });
      return p.slug;
    }
    return api.CreateProject({ ...input, columns: [] });
  });
  if (result && !p) location.hash = boardHref(result);
  else if (result) await render();
}

// milestoneDialog adds a milestone (m null) or edits one.
async function milestoneDialog(slug, m) {
  const changed = await showDialog(`<h2>${m ? 'Edit milestone' : 'New milestone'}</h2>
    <div class="field"><label for="m-title" class="lbl">Title</label><input id="m-title" name="title" class="input" value="${esc(m ? m.title : '')}" placeholder="For example SAT, or Delivery of panels" data-autofocus autocomplete="off"></div>
    <div class="field"><label for="m-date" class="lbl">Date</label><input id="m-date" name="date" type="date" class="input" value="${esc(m ? m.date : '')}"></div>
    <div class="field"><label for="m-desc" class="lbl">Description</label><textarea id="m-desc" name="description" class="ta">${esc(m ? m.description : '')}</textarea></div>
    ${m ? `<label style="display: flex; align-items: center; gap: 10px; min-height: 44px"><input type="checkbox" name="done" class="check" ${m.done ? 'checked' : ''}> Reached (done)</label>
      <p class="muted" style="font-size: 13px">${m.tasksTotal ? `${m.tasksDone} of ${plural(m.tasksTotal, 'linked task')} done.` : 'No tasks linked yet.'} Link tasks from each task's page.</p>` : '<p class="muted" style="font-size: 13px">Link tasks to it afterwards from each task\'s page.</p>'}
    <div class="actions">${m ? '<button type="button" class="btn btn-danger" data-dialog-action="delete" style="margin-right: auto">Delete</button>' : ''}
      <button type="button" class="btn btn-ghost" data-close>Cancel</button><button type="submit" class="btn btn-primary">${m ? 'Save' : 'Add milestone'}</button></div>`,
  async (fd) => {
    const input = { title: fd.get('title'), date: fd.get('date'), description: fd.get('description') };
    if (!String(input.title).trim()) throw new Error('Give the milestone a title.');
    if (!input.date) throw new Error('Choose a date.');
    if (m) await api.UpdateMilestone(slug, m.id, { ...input, done: fd.get('done') === 'on' });
    else await api.AddMilestone(slug, input);
  }, {
    delete: async () => {
      dlg.close();
      const ok = await confirmDialog(`Delete “${m.title}”?`, m.tasksTotal ? `${plural(m.tasksTotal, 'task')} linked to it will be unlinked. The tasks themselves are kept.` : 'This cannot be undone.', 'Delete', true);
      if (ok) await act(() => api.DeleteMilestone(slug, m.id));
      return false;
    },
  });
  if (changed) await render();
}

// ---------- Actions ----------

const actions = {
  toggleTheme() {
    saveSettings((s) => { s.theme = isDark() ? 'light' : 'dark'; }).catch((e) => toast(errText(e)));
    applyTheme();
  },
  toggleQuick() {
    saveSettings((s) => { s.quickList = !s.quickList; })
      .then(() => { renderQuick(); if (prefs().quickList) setTimeout(() => document.getElementById('quick-new')?.focus(), 50); })
      .catch((e) => toast(errText(e)));
  },
  async chooseLibrary() {
    try { state.settings = await api.ChooseLibrary(); } catch (e) { toast(errText(e)); }
    await render();
  },
  async retryLibrary() {
    try { state.settings = await api.RetryLibrary(); } catch (e) { toast(errText(e)); }
    await render();
  },
  showFolder(el) { api.ShowLibraryFolder(el.dataset.slug || '').catch((e) => toast(errText(e))); },
  newTask(el) { newTaskDialog(el.dataset.slug, el.dataset.status); },
  newProject() { projectDialog(null); },
  editProject() { if (state.data && state.data.project) projectDialog(state.data.project); },
  newMilestone() { milestoneDialog(parseRoute().slug, null); },
  editMilestone(el) {
    const m = (state.data.milestones || []).find((x) => x.id === Number(el.dataset.id));
    if (m) milestoneDialog(parseRoute().slug, m);
  },
  toggleColumn(el) {
    const slug = parseRoute().slug;
    act(() => saveSettings((s) => {
      s.collapsed = s.collapsed || {};
      const cols = new Set(s.collapsed[slug] || []);
      if (cols.has(el.dataset.col)) cols.delete(el.dataset.col); else cols.add(el.dataset.col);
      s.collapsed[slug] = [...cols];
    }));
  },
  toggleSubtasks(el) {
    const k = el.dataset.key;
    if (state.expanded.has(k)) state.expanded.delete(k); else state.expanded.add(k);
    render();
  },
  editNote(el) {
    const id = Number(el.dataset.note);
    const n = (state.data.notes || []).find((x) => x.id === id);
    state.editingNote = { key: el.dataset.key, id, text: n ? n.text : '' };
    render();
  },
  cancelNote() { state.editingNote = null; render(); },
  editTitle() { state.editTitle = state.data.task.title; render(); },
  cancelTitle() { state.editTitle = null; render(); },
  editDesc() { state.editDesc = state.data.task.description || ''; render(); },
  cancelDesc() { state.editDesc = null; render(); },
  clearDeadline() { const r = parseRoute(); act(() => api.UpdateTask(r.slug, r.id, { deadline: '' })); },
  markDone(el) {
    const r = parseRoute();
    act(() => withConfirm((all) => api.SetDone(r.slug, r.id, el.dataset.done === 'true', all)));
  },
  async deleteTask() {
    const r = parseRoute();
    const d = state.data;
    const n = (d.subtasks || []).length;
    const ok = await confirmDialog(`Delete “${d.task.title}”?`,
      `${n ? `Its ${plural(n, 'subtask')} will be deleted too. ` : ''}The files are moved to the .trash folder in your library, so you can still recover them there.`, 'Delete', true);
    if (!ok) return;
    const parent = (d.ancestors || []).slice(-1)[0];
    try {
      await api.DeleteTask(r.slug, r.id);
      location.hash = parent ? taskHref(r.slug, parent.id) : boardHref(r.slug);
    } catch (e) { toast(errText(e)); }
  },
  colMove(el) {
    const i = Number(el.dataset.i);
    const j = i + Number(el.dataset.dir);
    const c = state.colsDraft;
    [c[i], c[j]] = [c[j], c[i]];
    render();
  },
  colRemove(el) { state.colsDraft.splice(Number(el.dataset.i), 1); render(); },
  colAdd() { state.colsDraft.splice(state.colsDraft.length - 1, 0, { name: 'New column', type: 'active' }); render(); },
  colReset() { state.colsDraft = DEFAULT_COLUMNS.map((c) => ({ ...c })); render(); },
  colSave() {
    act(async () => {
      await saveSettings((s) => { s.projects.defaultColumns = state.colsDraft.map((c) => ({ id: '', name: c.name.trim(), type: c.type })); });
      state.colsDraft = null;
      toast('Columns saved. New projects will use them.');
    });
  },
};

const changes = {
  done(el) {
    act(() => withConfirm((all) => api.SetDone(el.dataset.slug, Number(el.dataset.id), el.checked, all)));
  },
  milestoneDone(el) { act(() => api.UpdateMilestone(el.dataset.slug, Number(el.dataset.id), { done: el.checked })); },
  status(el) { const r = parseRoute(); act(() => withConfirm((all) => api.UpdateTask(r.slug, r.id, { status: el.value, completeSubtasks: all }))); },
  priority(el) { const r = parseRoute(); act(() => api.UpdateTask(r.slug, r.id, { priority: el.value })); },
  deadline(el) { const r = parseRoute(); act(() => api.UpdateTask(r.slug, r.id, { deadline: el.value })); },
  waiting(el) { const r = parseRoute(); act(() => api.UpdateTask(r.slug, r.id, { waitingFor: el.value })); },
  milestone(el) { const r = parseRoute(); act(() => api.UpdateTask(r.slug, r.id, { milestone: Number(el.value) })); },
  pref(el) {
    const [group, name] = el.dataset.key.split('.');
    const value = el.type === 'checkbox' ? el.checked : el.value;
    act(() => saveSettings((s) => { if (name) s[group][name] = value; else s[group] = value; }));
  },
  coldraft(el) { state.colsDraft[Number(el.dataset.coldraft)][el.dataset.field] = el.value; },
};

const submits = {
  addNote(form) {
    const key = form.dataset.key;
    const text = state.drafts[key] || '';
    act(async () => {
      await api.AddNote(form.dataset.slug, Number(form.dataset.task), text);
      delete state.drafts[key];
    });
  },
  saveNote(form) {
    const ed = state.editingNote;
    act(async () => {
      await api.EditNote(form.dataset.slug, Number(form.dataset.task), Number(form.dataset.note), ed.text);
      state.editingNote = null;
    });
  },
  saveTitle() {
    const r = parseRoute();
    act(async () => { await api.UpdateTask(r.slug, r.id, { title: state.editTitle }); state.editTitle = null; });
  },
  saveDesc() {
    const r = parseRoute();
    act(async () => { await api.UpdateTask(r.slug, r.id, { description: state.editDesc }); state.editDesc = null; });
  },
  addSubtask(form) {
    const r = parseRoute();
    const input = form.querySelector('input');
    const title = input.value.trim();
    if (!title) return;
    act(async () => {
      await api.CreateTask(r.slug, { title, parent: r.id, priority: prefs().tasks.defaultPriority, status: '', deadline: '', description: '' });
      setTimeout(() => document.getElementById('new-sub')?.focus(), 0);
    });
  },
};

view.addEventListener('click', (e) => {
  const el = e.target.closest('[data-action]');
  if (el && actions[el.dataset.action]) { e.preventDefault(); actions[el.dataset.action](el, e); }
});
document.querySelector('.topbar').addEventListener('click', (e) => {
  const el = e.target.closest('[data-action]');
  if (el && actions[el.dataset.action]) actions[el.dataset.action](el, e);
});
view.addEventListener('change', (e) => {
  const el = e.target.closest('[data-change]');
  if (el && changes[el.dataset.change]) changes[el.dataset.change](el, e);
  if (e.target.dataset.coldraft !== undefined) changes.coldraft(e.target);
});
view.addEventListener('submit', (e) => {
  const form = e.target.closest('[data-submit]');
  if (!form) return;
  e.preventDefault();
  submits[form.dataset.submit](form, e);
});
view.addEventListener('input', (e) => {
  const el = e.target;
  if (el.dataset.draft !== undefined) {
    state.drafts[el.dataset.draft] = el.value;
    const btn = view.querySelector(`button[data-for="${CSS.escape(el.dataset.draft)}"]`);
    if (btn) btn.disabled = !el.value.trim();
  }
  if (el.dataset.coldraft !== undefined) changes.coldraft(el);
  if (el.dataset.edit === 'note' && state.editingNote) state.editingNote.text = el.value;
  if (el.dataset.edit === 'title') state.editTitle = el.value;
  if (el.dataset.edit === 'desc') state.editDesc = el.value;
});
view.addEventListener('keydown', (e) => {
  // Ctrl+Enter sends a note or saves an edit from a textarea.
  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey) && e.target.matches('textarea')) {
    e.preventDefault();
    e.target.form?.requestSubmit();
  }
  if (e.key === 'Escape' && e.target.closest('[data-submit="saveTitle"], [data-submit="saveDesc"], [data-submit="saveNote"]')) {
    state.editTitle = state.editDesc = state.editingNote = null;
    render();
  }
});
document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape' && !dlg.open && prefs() && prefs().quickList && !e.target.closest('#view form')) actions.toggleQuick();
});

// ---------- Board drag and drop ----------

let drag = null;

function clearDropMarks() {
  view.querySelectorAll('.drop-target, .drop-before').forEach((x) => x.classList.remove('drop-target', 'drop-before'));
}

view.addEventListener('dragstart', (e) => {
  const card = e.target.closest && e.target.closest('.bcard');
  if (!card) return;
  drag = { id: Number(card.dataset.id), status: null, before: 0 };
  card.classList.add('dragging');
  e.dataTransfer.effectAllowed = 'move';
  e.dataTransfer.setData('text/plain', card.dataset.id);
});
view.addEventListener('dragend', () => {
  view.querySelector('.dragging')?.classList.remove('dragging');
  clearDropMarks();
  drag = null;
});
view.addEventListener('dragover', (e) => {
  if (!drag) return;
  const col = e.target.closest('.column');
  if (!col) return;
  e.preventDefault();
  clearDropMarks();
  col.classList.add('drop-target');
  const before = [...col.querySelectorAll('.bcard:not(.dragging)')].find((c) => {
    const r = c.getBoundingClientRect();
    return e.clientY < r.top + r.height / 2;
  });
  if (before) before.classList.add('drop-before');
  drag.status = col.dataset.status;
  drag.before = before ? Number(before.dataset.id) : 0;
});
view.addEventListener('drop', (e) => {
  if (!drag || !drag.status) return;
  e.preventDefault();
  const { id, status, before } = drag;
  const slug = parseRoute().slug;
  clearDropMarks();
  act(() => withConfirm((all) => api.MoveTask(slug, id, status, before, all)));
});

// ---------- Start ----------

function typing() {
  const a = document.activeElement;
  const inForm = (root) => a && root.contains(a) && a.matches('textarea, input:not([type]), input[type="text"], input[type="date"]');
  return dlg.open || inForm(view) || inForm(quickEl);
}

function refreshWhenIdle() {
  setTimeout(() => {
    if (state.pendingRefresh && !typing()) { state.pendingRefresh = false; render(); }
  }, 0);
}
view.addEventListener('focusout', refreshWhenIdle);
quickEl.addEventListener('focusout', refreshWhenIdle);
dlg.addEventListener('close', refreshWhenIdle);

window.addEventListener('hashchange', render);

async function start() {
  state.settings = await api.GetSettings();
  applyTheme();
  // Files changed outside the app: redraw, but not while the user is typing.
  window.runtime.EventsOn('library-changed', () => {
    if (typing()) state.pendingRefresh = true; else render();
  });
  await render();
}

start();
