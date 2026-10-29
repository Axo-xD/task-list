'use strict';

// Calls into Go (app.go). Each returns a Promise; Go errors reject it with a message.
const api = new Proxy({}, { get: (_, name) => (...args) => window.go.main.App[name](...args) });

const appEl = document.getElementById('app');
const view = document.getElementById('view');
const dlg = document.getElementById('dlg');
const toastEl = document.getElementById('toast');

const state = {
  settings: { libraryPath: '', theme: '', lastProject: '' },
  routeKey: '',
  data: null,          // what the current view was drawn from
  drafts: {},          // unsent note text, by notes key
  editingNote: null,   // { key, id, text }
  editTitle: null,     // text while renaming the task
  editDesc: null,      // text while editing the description
  expanded: new Set(), // board cards showing their subtasks
  pendingRefresh: false,
};

const PALETTE = ['#2F5BEA', '#7C3AED', '#D97706', '#0E9384', '#DC2626', '#DB2777', '#4B5563', '#16A34A'];
const PRIORITY = { high: 'High', medium: 'Medium', low: 'Low' };

// ---------- Helpers ----------

const esc = (v) => String(v ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const color = (c) => (/^#[0-9a-fA-F]{3,8}$/.test(c || '') ? c : '#5B6270');
const plural = (n, word) => `${n} ${word}${n === 1 ? '' : 's'}`;
const taskHref = (slug, id) => `#/p/${encodeURIComponent(slug)}/t/${id}`;
const boardHref = (slug) => `#/p/${encodeURIComponent(slug)}`;

const icon = {
  cal: (s = 14) => `<svg aria-hidden="true" width="${s}" height="${s}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="18" rx="2"></rect><path d="M16 2v4M8 2v4M3 10h18"></path></svg>`,
  clock: `<svg aria-hidden="true" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"></circle><path d="M12 7v5l3 2"></path></svg>`,
  plus: (s = 14) => `<svg aria-hidden="true" width="${s}" height="${s}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><path d="M12 5v14M5 12h14"></path></svg>`,
  check: `<svg aria-hidden="true" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="var(--done-check)" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" style="flex: none; margin-top: 1px"><path d="M5 12l5 5L20 7"></path></svg>`,
  moon: `<svg aria-hidden="true" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z"></path></svg>`,
  sun: `<svg aria-hidden="true" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="4"></circle><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"></path></svg>`,
};

function parseDate(s) { // "2026-10-29" or "2026-10-29T09:05" → local Date
  const [y, m, d, h = 0, mi = 0] = s.split(/[-T:]/).map(Number);
  return new Date(y, m - 1, d, h, mi);
}
const todayStr = () => { const n = new Date(); return `${n.getFullYear()}-${String(n.getMonth() + 1).padStart(2, '0')}-${String(n.getDate()).padStart(2, '0')}`; };
const fmtDay = (s) => parseDate(s).toLocaleDateString('en-GB', { weekday: 'short', day: 'numeric', month: 'short' }).replace(',', '');
const fmtStamp = (s) => { const d = parseDate(s); return `${d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' })}, ${d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })}`; };
const fmtDate = (s) => parseDate(s).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' });

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

// ---------- Theme ----------

const media = window.matchMedia('(prefers-color-scheme: dark)');
const isDark = () => (state.settings.theme ? state.settings.theme === 'dark' : media.matches);

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
  if (parts[0] === 'p' && parts[1]) {
    if (parts[2] === 't' && Number(parts[3]) > 0) return { view: 'task', slug: parts[1], id: Number(parts[3]) };
    return { view: 'board', slug: parts[1] };
  }
  return { view: 'dashboard' };
}

async function render() {
  const route = parseRoute();
  const key = location.hash;
  const sameView = key === state.routeKey;
  if (!sameView) {
    state.editingNote = state.editTitle = state.editDesc = null;
    state.routeKey = key;
  }
  const hasLib = !!state.settings.libraryPath;
  document.getElementById('mainnav').hidden = !hasLib;
  document.getElementById('new-task-btn').hidden = !hasLib;
  document.querySelectorAll('[data-nav]').forEach((a) => {
    const on = a.dataset.nav === route.view || (a.dataset.nav === 'projects' && (route.view === 'board' || route.view === 'task'));
    if (on) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current');
  });
  if (!hasLib) { view.innerHTML = welcomeHTML(); return; }

  const scrollY = window.scrollY;
  const boardX = document.querySelector('.board-scroll')?.scrollLeft || 0;
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
    view.innerHTML = `<div class="page"><div class="card card-pad"><h1 style="font-size: 20px">Not found</h1><p class="muted" style="margin: 8px 0 16px">${esc(errText(e))}</p><a class="btn btn-ghost" href="#/">Back to dashboard</a></div></div>`;
  }
  if (sameView) {
    window.scrollTo(0, scrollY);
    const b = document.querySelector('.board-scroll');
    if (b) b.scrollLeft = boardX;
  } else {
    window.scrollTo(0, 0);
  }
  const auto = view.querySelector('[data-autofocus]');
  if (auto) { auto.focus(); auto.select?.(); }
}

function welcomeHTML() {
  return `<div class="card welcome">
    <h1>Welcome to Tasks</h1>
    <p>Choose the folder where your projects will be kept. Each project becomes its own folder of plain text files, so you can copy, zip, move or back it up like any other folder.</p>
    <p>Pick an empty folder to start fresh, or a folder that already holds your projects.</p>
    <div><button type="button" class="btn btn-primary" data-action="chooseLibrary">Choose folder</button></div>
  </div>`;
}

function problemsBanner(problems) {
  if (!problems.length) return '';
  return `<div class="banner" role="status"><span><strong>${plural(problems.length, 'file')}</strong> could not be read as expected.</span><a href="#/projects">See details</a></div>`;
}

const dotHTML = (c, size = 10) => `<span class="dot" aria-hidden="true" style="width: ${size}px; height: ${size}px; background: ${color(c)}"></span>`;
const priChip = (p) => `<span class="chip pri-${esc(p)}">${PRIORITY[p] || esc(p)}</span>`;

// ---------- Dashboard ----------

function dashboardHTML(d, problems) {
  const now = new Date();
  const hour = now.getHours();
  const greeting = hour < 12 ? 'Good morning' : hour < 18 ? 'Good afternoon' : 'Good evening';
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
        <p>Projects hold your tasks, a board with columns and a notes log. Create your first one to get started.</p>
        <div><button type="button" class="btn btn-primary" data-action="newProject">${icon.plus(16)} New project</button></div>
      </div></div>`;
  }

  const important = groups.length ? groups.map((g) => `
    <div style="margin-top: 22px">
      <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 10px">
        <span class="grp g-${g.key}">${esc(g.label)}</span><span class="count">${g.tasks.length}</span>
      </div>
      <ul class="tasklist">${g.tasks.map((t) => {
        const dd = due(t.deadline);
        return `<li class="taskrow">
          <input type="checkbox" class="check" data-change="done" data-slug="${esc(t.project.slug)}" data-id="${t.id}" aria-label="Mark ${esc(t.title)} as done">
          <div style="flex: 1 1 220px; min-width: 0">
            <a class="title" href="${taskHref(t.project.slug, t.id)}">${esc(t.title)}</a>
            <div class="meta">${dotHTML(t.project.color)}<span>${esc(t.project.name)}</span>
              ${t.parentTitle ? `<span aria-hidden="true">·</span><span>in ${esc(t.parentTitle)}</span>` : ''}
              <span aria-hidden="true">·</span><span>${esc(t.statusName)}</span></div>
          </div>
          <span class="due-text ${dd.cls}">${icon.cal()}${esc(dd.text)}</span>
          ${priChip(t.priority)}
        </li>`;
      }).join('')}</ul>
    </div>`).join('') : `<p class="empty" style="margin-top: 22px">No open tasks with a deadline.</p>`;

  const active = (d.active || []).length ? d.active.map((p) => `
    <div style="margin-top: 22px">
      <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 8px">
        ${dotHTML(p.color)}<h3 style="font-size: 14px; font-weight: 700; flex: 1">${esc(p.name)}</h3>
        <a href="${boardHref(p.slug)}" style="font-size: 13px; font-weight: 600; text-decoration: none; padding: 6px 0">Open board</a>
      </div>
      <ul class="activelist">${p.tasks.map((t) => `<li><a href="${taskHref(p.slug, t.id)}">${esc(t.title)}</a><span class="chip st-${esc(t.tone)}">${esc(t.statusName)}</span></li>`).join('')}</ul>
    </div>`).join('') : `<p class="empty" style="margin-top: 22px">Nothing in progress right now.</p>`;

  return `<div class="page">
    ${problemsBanner(problems)}
    <div class="greeting">
      <p class="date">${esc(now.toLocaleDateString('en-GB', { weekday: 'long', day: 'numeric', month: 'long' }))}</p>
      <h1>${greeting}</h1>
      <p>${summary}</p>
    </div>
    <div class="row">
      <section class="card card-pad" aria-labelledby="imp-h" style="flex: 3 1 520px; min-width: 0">
        <div class="section-head"><h2 id="imp-h">Important tasks</h2><span class="muted" style="font-size: 13px">Sorted by deadline, then priority</span></div>
        ${important}
      </section>
      <section class="card card-pad" aria-labelledby="act-h" style="flex: 2 1 340px; min-width: 0">
        <div class="section-head"><h2 id="act-h">Active tasks</h2><span class="muted" style="font-size: 13px">Tasks in active columns</span></div>
        ${active}
      </section>
    </div>
    <section aria-labelledby="proj-h">
      <div class="section-head" style="margin-bottom: 12px"><h2 id="proj-h">Projects</h2><a href="#/projects" style="font-size: 14px; font-weight: 600; text-decoration: none">View all</a></div>
      <div class="projgrid">${projects.map(projectCardHTML).join('')}</div>
    </section>
  </div>`;
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
    <div class="section-head"><h1 style="font-size: 28px; font-weight: 700; letter-spacing: -0.02em">Projects</h1>
      <button type="button" class="btn btn-primary" data-action="newProject">${icon.plus(16)} New project</button></div>
    <div class="projgrid">${open.map(projectCardHTML).join('')}
      <button type="button" class="newcard" data-action="newProject">${icon.plus(16)} New project</button></div>
    ${archived.length ? `<section aria-labelledby="arch-h"><h2 id="arch-h" style="font-size: 18px; font-weight: 700; margin-bottom: 12px">Archived</h2>
      <div class="projgrid">${archived.map(projectCardHTML).join('')}</div></section>` : ''}
    <section class="card card-pad" aria-labelledby="lib-h">
      <h2 id="lib-h" style="font-size: 16px; font-weight: 700">Library folder</h2>
      <p style="margin: 8px 0 16px; font-size: 14px; color: var(--text2); word-break: break-all">${esc(state.settings.libraryPath)}</p>
      <div style="display: flex; flex-wrap: wrap; gap: 8px">
        <button type="button" class="btn btn-ghost" data-action="showFolder" data-slug="">Show in Explorer</button>
        <button type="button" class="btn btn-ghost" data-action="chooseLibrary">Change folder</button>
      </div>
      ${problems.length ? `<h3 style="font-size: 14px; font-weight: 700; margin-top: 20px; color: var(--warn-text)">Files with problems</h3>
        <p class="muted" style="font-size: 13px; margin-top: 4px">The app does not change these files. Fix them in a text editor and they are picked up automatically.</p>
        <ul class="problems">${problems.map((p) => `<li><code>${esc(p.path)}</code><br>${esc(p.message)}</li>`).join('')}</ul>` : ''}
    </section>
  </div>`;
}

// ---------- Board ----------

function boardHTML(b) {
  const p = b.project;
  const firstOpen = (b.columns.find((c) => c.type === 'not_started') || b.columns[0] || {}).id;
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
        <div style="min-width: 220px">
          <div style="display: flex; justify-content: space-between; font-size: 13px; color: var(--muted); margin-bottom: 6px"><span>${p.done} of ${p.total} done</span><span>${p.pct}%</span></div>
          <div class="bar" style="background: var(--border)"><div style="width: ${p.pct}%"></div></div>
        </div>
        <button type="button" class="btn btn-ghost" data-action="editProject">Edit project</button>
        <button type="button" class="btn btn-ghost" data-action="showFolder" data-slug="${esc(p.slug)}">Show folder</button>
      </div>
    </div>
    <div class="board-scroll"><div class="board">
      ${b.columns.map((col) => `
        <section class="column" data-status="${esc(col.id)}" aria-label="${esc(col.name)}">
          <div class="column-head"><h2>${esc(col.name)}</h2><span class="count">${(col.cards || []).length}</span></div>
          ${(col.cards || []).map((c) => cardHTML(p.slug, c)).join('')}
          ${col.id === firstOpen ? `<button type="button" class="addcard" data-action="newTask" data-slug="${esc(p.slug)}" data-status="${esc(col.id)}">${icon.plus()} Add task</button>` : ''}
        </section>`).join('')}
    </div></div>
    ${notesHTML({ key: `p:${p.slug}`, slug: p.slug, task: 0, notes: b.notes, title: 'Project notes', hint: 'Notes for the whole project, newest first. Task notes live on each task.' })}
  </div>`;
}

function cardHTML(slug, c) {
  if (c.done) {
    return `<article class="card bcard" draggable="true" data-id="${c.id}"><div class="donecard">${icon.check}<div>
      <a class="title" href="${taskHref(slug, c.id)}">${esc(c.title)}</a>
      ${c.completed ? `<div class="muted" style="font-size: 12px; margin-top: 4px">Completed ${esc(fmtDay(c.completed.slice(0, 10)))}</div>` : ''}
    </div></div></article>`;
  }
  const key = `${slug}:${c.id}`;
  const open = state.expanded.has(key);
  const pct = c.subTotal ? Math.round((c.subDone / c.subTotal) * 100) : 0;
  const dd = c.deadline ? due(c.deadline) : null;
  return `<article class="card bcard" draggable="true" data-id="${c.id}">
    <a class="title" href="${taskHref(slug, c.id)}">${esc(c.title)}</a>
    ${c.waitingFor ? `<div class="waiting">${icon.clock}Waiting for: ${esc(c.waitingFor)}</div>` : ''}
    ${c.subTotal ? `<div>
      <div class="subprog-head"><span>Subtasks ${c.subDone}/${c.subTotal}</span>
        <button type="button" class="btn-link" data-action="toggleSubtasks" data-key="${esc(key)}" aria-expanded="${open}">${open ? 'Hide' : 'Show'}</button></div>
      <div class="bar thin"><div style="width: ${pct}%"></div></div>
    </div>` : ''}
    ${open ? `<ul class="sublist">${(c.subtasks || []).map((s) => `<li style="padding-left: ${s.depth * 24}px">
      <input type="checkbox" class="check" style="width: 16px; height: 16px" data-change="done" data-slug="${esc(slug)}" data-id="${s.id}" ${s.done ? 'checked' : ''} aria-label="${esc(s.title)} done">
      <a href="${taskHref(slug, s.id)}" style="color: ${s.done ? 'var(--muted)' : 'var(--text)'}; text-decoration: none">${esc(s.title)}</a></li>`).join('')}</ul>` : ''}
    <div class="chips">
      ${dd ? `<span class="chip due-${dd.cls}">${icon.cal(12)}${esc(dd.cls === 'over' ? fmtDay(c.deadline) : dd.text)}</span>` : ''}
      ${priChip(c.priority)}
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
          <div class="chips" style="gap: 8px"><span class="chip st-${esc(t.tone)}">${esc(t.statusName)}</span>${priChip(t.priority)}${dueChip}</div></div>
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

// ---------- Dialogs ----------

// showDialog opens the dialog with a form. onSubmit gets the form data; if it
// throws, the message is shown in the dialog and it stays open.
function showDialog(html, onSubmit) {
  dlg.innerHTML = `<form class="dialog-body" method="dialog" novalidate>${html}<p class="dlg-error" role="alert" style="color: var(--danger); font-size: 13px" hidden></p></form>`;
  dlg.showModal();
  dlg.querySelector('[data-autofocus]')?.focus();
  return new Promise((resolve) => {
    const form = dlg.querySelector('form');
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      try {
        const result = await onSubmit(new FormData(form));
        resolve(result === undefined ? true : result);
        dlg.close();
      } catch (err) {
        const p = form.querySelector('.dlg-error');
        p.textContent = errText(err);
        p.hidden = false;
      }
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
  const want = slug || route.slug || state.settings.lastProject;
  const chosen = projects.some((p) => p.slug === want) ? want : projects[0].slug;
  const created = await showDialog(`<h2>New task</h2>
    <div class="field"><label for="q-title" class="lbl">Title</label><input id="q-title" name="title" class="input" data-autofocus autocomplete="off"></div>
    <div class="field"><label for="q-project" class="lbl">Project</label>
      <select id="q-project" name="project" class="select" ${slug ? 'disabled' : ''}>${projects.map((p) => `<option value="${esc(p.slug)}" ${p.slug === chosen ? 'selected' : ''}>${esc(p.name)}</option>`).join('')}</select></div>
    <div class="two">
      <div class="field"><label for="q-pri" class="lbl">Priority</label><select id="q-pri" name="priority" class="select">
        <option value="medium">Medium</option><option value="high">High</option><option value="low">Low</option></select></div>
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
    return api.CreateProject(input);
  });
  if (result && !p) location.hash = boardHref(result);
  else if (result) await render();
}

// ---------- Actions ----------

const actions = {
  toggleTheme() {
    state.settings.theme = isDark() ? 'light' : 'dark';
    applyTheme();
    api.SetTheme(state.settings.theme);
  },
  async chooseLibrary() {
    try {
      state.settings = await api.ChooseLibrary();
    } catch (e) { toast(errText(e)); }
    await render();
  },
  showFolder(el) { api.ShowLibraryFolder(el.dataset.slug || '').catch((e) => toast(errText(e))); },
  newTask(el) { newTaskDialog(el.dataset.slug, el.dataset.status); },
  newProject() { projectDialog(null); },
  editProject() { if (state.data && state.data.project) projectDialog(state.data.project); },
  toggleSubtasks(el) {
    const k = el.dataset.key;
    if (state.expanded.has(k)) state.expanded.delete(k); else state.expanded.add(k);
    render();
  },
  editNote(el) {
    const id = Number(el.dataset.note);
    const notes = state.data.notes || [];
    const n = notes.find((x) => x.id === id);
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
};

const changes = {
  done(el) {
    act(() => withConfirm((all) => api.SetDone(el.dataset.slug, Number(el.dataset.id), el.checked, all)));
  },
  status(el) { const r = parseRoute(); act(() => withConfirm((all) => api.UpdateTask(r.slug, r.id, { status: el.value, completeSubtasks: all }))); },
  priority(el) { const r = parseRoute(); act(() => api.UpdateTask(r.slug, r.id, { priority: el.value })); },
  deadline(el) { const r = parseRoute(); act(() => api.UpdateTask(r.slug, r.id, { deadline: el.value })); },
  waiting(el) { const r = parseRoute(); act(() => api.UpdateTask(r.slug, r.id, { waitingFor: el.value })); },
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
      await api.CreateTask(r.slug, { title, parent: r.id, priority: 'medium', status: '', deadline: '', description: '' });
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
  return dlg.open || (a && view.contains(a) && a.matches('textarea, input:not([type]), input[type="text"], input[type="date"]'));
}

view.addEventListener('focusout', () => {
  setTimeout(() => {
    if (state.pendingRefresh && !typing()) { state.pendingRefresh = false; render(); }
  }, 0);
});
dlg.addEventListener('close', () => {
  if (state.pendingRefresh) { state.pendingRefresh = false; render(); }
});

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
