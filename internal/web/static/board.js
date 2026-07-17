"use strict";

const $ = (sel) => document.querySelector(sel);

const COLUMNS = [
  { status: "pending", label: "Pending" },
  { status: "in-progress", label: "In progress" },
  { status: "done", label: "Done" },
];
const DONE_CAP = 15;

const state = {
  project: localStorage.getItem("taskman.project") || "",
  lane: "",
  showDeferred: false,
  swimlanes: false,
  showAllDone: false,
  tasks: [],
  projects: [],
  dialogTask: null,
};

async function api(path, opts) {
  const res = await fetch(path, opts);
  if (res.status === 204) return null;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

function post(path, body) {
  return api(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body || {}),
  });
}

async function mutate(fn) {
  try {
    await fn();
  } catch (err) {
    alert(err.message || err);
  }
  await loadTasks();
  // A mutation can start from another view's surface (a feature chip's
  // dialog, a decisions-inbox row); refresh whichever is visible so it
  // reflects the change without a tab switch. typeof-guarded: those flags
  // live in scripts loaded after this one.
  if (typeof featuresVisible !== "undefined" && featuresVisible) {
    await loadFeatures();
  }
  if (typeof decisionsVisible !== "undefined" && decisionsVisible) {
    await loadDecisions();
  }
}

async function loadProjects() {
  const projects = await api("/api/projects");
  state.projects = projects;
  // The hidden select stays the state holder: its value and change event
  // are the contract board.js and features.js already listen on; the
  // picker below is only the visible UI.
  const sel = $("#project");
  sel.replaceChildren();
  for (const p of projects) sel.append(new Option(p.name, p.name));
  if (!projects.some((p) => p.name === state.project)) {
    state.project = projects[0] ? projects[0].name : "";
  }
  sel.value = state.project;
  updateProjectButton();
}

// --- searchable project picker (ctrl/cmd+k): filters as you type, busy
// projects first, idle ones dimmed; selection flows through the hidden
// select's change event.
let pickerIndex = 0;

function updateProjectButton() {
  const p = state.projects.find((x) => x.name === state.project);
  $("#project-button").textContent = p ? `${p.name} (${p.open})` : state.project || "no projects";
}

// pickerMatches ranks an exact name first (Enter takes the highlighted row,
// and typing a full name must never land on a busier prefix-sibling), then
// prefix matches, then the rest by activity.
function pickerMatches(q) {
  const needle = q.trim().toLowerCase();
  const rank = (p) => (p.name === needle ? 2 : p.name.startsWith(needle) ? 1 : 0);
  return state.projects
    .filter((p) => p.name.includes(needle))
    .sort((a, b) => rank(b) - rank(a) || b.open - a.open || a.name.localeCompare(b.name));
}

function renderPicker() {
  const list = $("#picker-list");
  const matches = pickerMatches($("#picker-search").value);
  pickerIndex = Math.min(pickerIndex, Math.max(0, matches.length - 1));
  list.replaceChildren();
  matches.forEach((p, i) => {
    const li = document.createElement("li");
    if (p.open === 0 && p.deferred === 0) li.classList.add("dim");
    if (i === pickerIndex) li.classList.add("active");
    const name = document.createElement("span");
    name.textContent = p.name;
    li.append(name);
    const counts = document.createElement("span");
    counts.className = "counts";
    counts.textContent = p.deferred ? `${p.open} open · ${p.deferred} deferred` : `${p.open} open`;
    li.append(counts);
    li.addEventListener("click", () => selectProject(p.name));
    list.append(li);
  });
  if (!matches.length) {
    const li = document.createElement("li");
    li.className = "dim";
    li.textContent = "no matching projects";
    list.append(li);
  }
}

function openPicker() {
  pickerIndex = 0;
  $("#picker-search").value = "";
  $("#picker-panel").hidden = false;
  renderPicker();
  $("#picker-search").focus();
}

function closePicker() {
  $("#picker-panel").hidden = true;
}

function selectProject(name) {
  closePicker();
  const sel = $("#project");
  sel.value = name;
  sel.dispatchEvent(new Event("change"));
  updateProjectButton();
}

function wirePicker() {
  $("#project-button").addEventListener("click", () => {
    if ($("#picker-panel").hidden) openPicker();
    else closePicker();
  });
  $("#picker-search").addEventListener("input", () => {
    pickerIndex = 0;
    renderPicker();
  });
  $("#picker-search").addEventListener("keydown", (e) => {
    const matches = pickerMatches($("#picker-search").value);
    if (e.key === "ArrowDown") {
      e.preventDefault();
      pickerIndex = Math.min(pickerIndex + 1, matches.length - 1);
      renderPicker();
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      pickerIndex = Math.max(pickerIndex - 1, 0);
      renderPicker();
    } else if (e.key === "Enter") {
      e.preventDefault();
      if (matches[pickerIndex]) selectProject(matches[pickerIndex].name);
    } else if (e.key === "Escape") {
      closePicker();
    }
  });
  document.addEventListener("click", (e) => {
    if (!e.target.closest(".picker")) closePicker();
  });
  document.addEventListener("keydown", (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
      // With the modal task dialog open the page behind is inert, so the
      // picker would open unusable behind the backdrop and stay stuck open
      // after the dialog closes.
      if ($("#task-dialog").open) return;
      e.preventDefault();
      openPicker();
    }
  });
}

async function loadTasks() {
  if (!state.project) return;
  const data = await api(`/api/projects/${state.project}/tasks`);
  state.tasks = data.tasks;
  const counts = {};
  for (const t of data.tasks) counts[t.num] = (counts[t.num] || 0) + 1;
  state.dupNums = new Set(
    Object.keys(counts)
      .filter((n) => counts[n] > 1)
      .map(Number)
  );
  updateDecisionsPill().catch(() => {});
  const sel = $("#lane");
  const current = state.lane;
  sel.replaceChildren(new Option("all lanes", ""));
  for (const lane of data.lanes) sel.append(new Option(lane, lane));
  sel.value = data.lanes.includes(current) ? current : "";
  state.lane = sel.value;
  render();
}

function visible(t) {
  if (state.lane && t.lane !== state.lane) return false;
  if (t.deferred && !state.showDeferred) return false;
  return true;
}

// --- drag and drop: across columns = status change, within pending =
// priority reorder. Deferred cards are not draggable; their moves are
// deliberate dialog actions.
const drag = { num: null, status: null };

function draggableCard(el, t) {
  el.draggable = !t.deferred;
  el.addEventListener("dragstart", (e) => {
    drag.num = t.num;
    drag.status = t.status;
    el.classList.add("dragging");
    e.dataTransfer.effectAllowed = "move";
  });
  el.addEventListener("dragend", () => {
    drag.num = null;
    el.classList.remove("dragging");
    clearDropHints();
  });
  el.addEventListener("dragover", (e) => {
    if (drag.num === null || drag.num === t.num) return;
    if (drag.status === "pending" && t.status === "pending") {
      e.preventDefault();
      e.stopPropagation();
      clearDropHints();
      el.classList.add("drop-above");
    }
  });
  el.addEventListener("drop", (e) => {
    if (drag.num === null || drag.status !== "pending" || t.status !== "pending") return;
    e.preventDefault();
    e.stopPropagation();
    reorderBefore(drag.num, t.num);
  });
}

function clearDropHints() {
  for (const el of document.querySelectorAll(".drop-above")) el.classList.remove("drop-above");
  for (const el of document.querySelectorAll(".drop-target")) el.classList.remove("drop-target");
}

// reorderBefore moves dragged in front of target in the full pending order
// (including tasks hidden by the lane filter, whose relative positions are
// preserved), then persists the whole list.
function reorderBefore(dragged, target) {
  const pending = state.tasks.filter((t) => t.status === "pending" && !t.deferred).map((t) => t.num);
  const without = pending.filter((n) => n !== dragged);
  const at = without.indexOf(target);
  if (at < 0) return;
  without.splice(at, 0, dragged);
  mutate(() =>
    api(`/api/projects/${state.project}/order`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ order: without }),
    })
  );
}

function columnDropZone(colEl, status) {
  colEl.addEventListener("dragover", (e) => {
    if (drag.num === null) return;
    if (drag.status !== status) {
      e.preventDefault();
      clearDropHints();
      colEl.classList.add("drop-target");
    } else if (status === "pending") {
      // Dropping on empty column space appends to the bottom.
      e.preventDefault();
    }
  });
  colEl.addEventListener("drop", (e) => {
    if (drag.num === null) return;
    e.preventDefault();
    clearDropHints();
    if (drag.status !== status) {
      mutate(() => post(`/api/projects/${state.project}/tasks/${drag.num}/status`, { status }));
    } else if (status === "pending") {
      const pending = state.tasks
        .filter((t) => t.status === "pending" && !t.deferred && t.num !== drag.num)
        .map((t) => t.num);
      pending.push(drag.num);
      mutate(() =>
        api(`/api/projects/${state.project}/order`, {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ order: pending }),
        })
      );
    }
  });
}

// card stays a draggable div (a raw button would disturb HTML5 DnD), so
// keyboard access is grafted on: focusable, role=button, Enter/Space opens
// the detail dialog. Dragging itself remains pointer-only by design.
// pushToTop rewrites the priority order with num first, through the same
// PUT (and single commit) as a drag. The order sent is the pending column's
// current sequence, so unlisted tasks keep their relative order; pressing
// it on the already-top task is a no-op, not an empty commit.
function pushToTop(num) {
  const pending = state.tasks
    .filter((t) => t.status === "pending" && !t.deferred)
    .map((t) => t.num);
  if (pending[0] === num) return;
  const order = [num, ...pending.filter((n) => n !== num)];
  mutate(() =>
    api(`/api/projects/${state.project}/order`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ order }),
    })
  ).then(() => focusAfterRender(`#board [data-num="${num}"]`, "#tab-tasks"));
}

// pushToBottom mirrors pushToTop: drag reordering is insert-before, so the
// very bottom has no drop target and needs its own control.
function pushToBottom(num) {
  const pending = state.tasks
    .filter((t) => t.status === "pending" && !t.deferred)
    .map((t) => t.num);
  if (pending[pending.length - 1] === num) return;
  const order = [...pending.filter((n) => n !== num), num];
  mutate(() =>
    api(`/api/projects/${state.project}/order`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ order }),
    })
  ).then(() => focusAfterRender(`#board [data-num="${num}"]`, "#tab-tasks"));
}

// stemOf strips status suffixes and the extension, leaving the ledger stem
// -- the key that can still resolve one half of a duplicate-numbered pair
// when the bare number is ambiguous.
function stemOf(file) {
  return file.replace(/(\.in-progress|\.done)?(\.deferred)?\.md$/, "");
}

function card(t) {
  const isDup = state.dupNums && state.dupNums.has(t.num);
  const openKey = isDup ? stemOf(t.file) : t.num;
  const el = document.createElement("div");
  el.className = "card" + (t.deferred ? " deferred" : "");
  el.dataset.num = t.num;
  el.dataset.status = t.status;
  el.tabIndex = 0;
  el.setAttribute("role", "button");
  el.addEventListener("keydown", (e) => {
    // Only the card's own keys: an Enter on the to-top button bubbles here
    // and must not also open the dialog.
    if (e.target !== el) return;
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      openTask(openKey).catch((err) => alert(err.message || err));
    }
  });
  draggableCard(el, t);

  const meta = document.createElement("div");
  meta.className = "meta";
  const num = document.createElement("span");
  num.className = "num mono";
  num.textContent = String(t.num).padStart(3, "0");
  meta.append(num);
  if (t.lane) {
    const lane = document.createElement("span");
    lane.className = "badge lane";
    lane.textContent = t.lane;
    meta.append(lane);
  }
  if (isDup) {
    const dup = document.createElement("span");
    dup.className = "badge duplicate";
    dup.textContent = "duplicate — run taskman fix";
    meta.append(dup);
  }
  if (t.has_decision) {
    const dec = document.createElement("span");
    dec.className = "badge decision";
    dec.textContent = "decision needed";
    meta.append(dec);
  } else if (t.deferred) {
    const def = document.createElement("span");
    def.className = "badge deferred";
    def.textContent = "deferred";
    meta.append(def);
  }
  if (t.status === "pending" && !t.deferred) {
    // Stacked vertically -- up above, down below -- to match their meaning.
    const controls = document.createElement("div");
    controls.className = "priority-controls";
    const top = document.createElement("button");
    top.type = "button";
    top.className = "to-top";
    top.textContent = "⤒";
    top.title = "move to top of priority";
    top.setAttribute("aria-label", `move task ${t.num} to top of priority`);
    top.addEventListener("click", (e) => {
      e.stopPropagation();
      pushToTop(t.num);
    });
    controls.append(top);
    const bottom = document.createElement("button");
    bottom.type = "button";
    bottom.className = "to-bottom";
    bottom.textContent = "⤓";
    bottom.title = "move to bottom of priority";
    bottom.setAttribute("aria-label", `move task ${t.num} to bottom of priority`);
    bottom.addEventListener("click", (e) => {
      e.stopPropagation();
      pushToBottom(t.num);
    });
    controls.append(bottom);
    meta.append(controls);
  }
  el.append(meta);

  const title = document.createElement("div");
  title.textContent = t.title;
  el.append(title);

  el.addEventListener("click", () => openTask(openKey).catch((err) => alert(err.message || err)));
  return el;
}

function appendCards(colEl, tasks) {
  if (state.swimlanes) {
    const lanes = [...new Set(tasks.map((t) => t.lane))].sort();
    for (const lane of lanes) {
      const head = document.createElement("div");
      head.className = "lane-head";
      head.textContent = lane || "no lane";
      colEl.append(head);
      for (const t of tasks.filter((x) => x.lane === lane)) colEl.append(card(t));
    }
    return;
  }
  for (const t of tasks) colEl.append(card(t));
}

function render() {
  const board = $("#board");
  board.replaceChildren();
  for (const col of COLUMNS) {
    let tasks = state.tasks.filter((t) => t.status === col.status && visible(t));
    const colEl = document.createElement("section");
    colEl.className = "column " + col.status;
    colEl.dataset.status = col.status;
    columnDropZone(colEl, col.status);

    const head = document.createElement("h2");
    head.textContent = col.label;
    const count = document.createElement("span");
    count.className = "count";
    count.textContent = String(tasks.length);
    head.append(count);
    colEl.append(head);

    const overCap = col.status === "done" && tasks.length > DONE_CAP;
    if (overCap && !state.showAllDone) {
      tasks = tasks.slice(-DONE_CAP).reverse();
    } else if (col.status === "done") {
      tasks = [...tasks].reverse();
    }
    appendCards(colEl, tasks);

    if (overCap) {
      const toggle = document.createElement("button");
      toggle.className = "show-more";
      toggle.textContent = state.showAllDone ? "show fewer" : "show all done";
      toggle.addEventListener("click", () => {
        state.showAllDone = !state.showAllDone;
        render();
      });
      colEl.append(toggle);
    }
    if (!tasks.length) {
      const empty = document.createElement("div");
      empty.className = "empty";
      empty.textContent = "nothing here";
      colEl.append(empty);
    }
    board.append(colEl);
  }
}

async function openTask(key) {
  // Direct opens (cards, chips, search) drop any decisions-view return
  // marker; router-driven opens keep it, since a decisions row travels
  // through a hash change to get here.
  if (typeof applyingHash === "undefined" || !applyingHash) {
    state.decisionsReturn = null;
  }
  const data = await api(`/api/projects/${state.project}/tasks/${key}`);
  // The dialog tracks the resolved number, not the lookup key: stems open
  // duplicate-numbered tasks, but hash sync and focus want the number.
  state.dialogTask = data.task.num;
  state.dialogData = data;
  $("#dialog-file").textContent = data.task.file;
  $("#dialog-body").innerHTML = data.html;
  if (data.decision) renderDecision(data.task, data.decision);
  renderActions(data.task);
  $("#task-dialog").showModal();
}

// renderDecision puts the structured question at the top of the dialog,
// mirroring an agent's option prompt: the question, one button per option
// with its explanation beneath the label, and a free-text Other when
// allowed. Answering returns the task to pending at the top of the queue.
function renderDecision(t, d) {
  const box = document.createElement("div");
  box.className = "decision-box";
  const q = document.createElement("div");
  q.className = "decision-question";
  q.textContent = d.question;
  box.append(q);

  const answer = (payload) => {
    $("#task-dialog").close();
    mutate(() => post(`/api/projects/${state.project}/tasks/${t.num}/answer`, payload)).then(() => {
      // A row-originated answer returns to the decisions list (now fresh);
      // otherwise stay on the board with focus on the task.
      const ret = state.decisionsReturn;
      state.decisionsReturn = null;
      if (ret) {
        location.hash = ret;
        return;
      }
      focusTask(t.num);
    });
  };
  for (const opt of d.options) {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "decision-option";
    const label = document.createElement("strong");
    label.textContent = opt.label;
    btn.append(label);
    if (opt.explain) {
      const explain = document.createElement("span");
      explain.className = "decision-explain";
      explain.textContent = opt.explain;
      btn.append(explain);
    }
    btn.addEventListener("click", () => answer({ choice: opt.label }));
    box.append(btn);
  }
  if (d.allow_other) {
    const row = document.createElement("div");
    row.className = "decision-other";
    const input = document.createElement("input");
    input.placeholder = "other...";
    row.append(input);
    const go = document.createElement("button");
    go.type = "button";
    go.textContent = "answer";
    const submit = () => {
      if (input.value.trim()) answer({ other: input.value.trim() });
    };
    go.addEventListener("click", submit);
    input.addEventListener("keydown", (e) => {
      if (e.key === "Enter") submit();
    });
    row.append(go);
    box.append(row);
  }
  $("#dialog-body").prepend(box);
}

// renderEditor swaps the dialog into edit mode: title input + raw markdown
// textarea, saved through PUT tasks/{n} (one scoped commit) and re-rendered
// in place.
function renderEditor(data) {
  const body = $("#dialog-body");
  body.replaceChildren();
  const titleInput = document.createElement("input");
  titleInput.id = "edit-title";
  titleInput.defaultValue = data.task.title;
  titleInput.value = data.task.title;
  titleInput.title = "task title (changes the slug/filename)";
  body.append(titleInput);
  const ta = document.createElement("textarea");
  ta.id = "edit-body";
  ta.defaultValue = data.body;
  ta.value = data.body;
  body.append(ta);

  const bar = $("#dialog-actions");
  bar.replaceChildren();
  const save = document.createElement("button");
  save.textContent = "save";
  save.addEventListener("click", async () => {
    const payload = { body: ta.value, base: data.etag };
    const title = titleInput.value.trim();
    if (title && title !== data.task.title) payload.title = title;
    try {
      await api(`/api/projects/${state.project}/tasks/${data.task.num}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
    } catch (err) {
      // Stay in the editor so a conflict (409) or any other failure never
      // discards what the user typed; cancel reloads the fresh version.
      alert(err.message || err);
      return;
    }
    await loadTasks().catch(showError);
    await openTask(data.task.num).catch((err) => alert(err.message || err));
  });
  bar.append(save);
  const cancel = document.createElement("button");
  cancel.textContent = "cancel";
  cancel.addEventListener("click", () =>
    openTask(data.task.num).catch((err) => alert(err.message || err))
  );
  bar.append(cancel);
  titleInput.focus();
}

// --- screenshots: paste or drop an image while the task dialog is open.
// The server stores it under <project>/screenshots/NNN/ and links it from
// the task body, so it renders inline on the next open.
async function uploadScreenshot(file) {
  const fd = new FormData();
  fd.append("file", file);
  const res = await fetch(
    `/api/projects/${state.project}/tasks/${state.dialogTask}/screenshots`,
    { method: "POST", body: fd }
  );
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data.error || res.statusText);
  }
  await openTask(state.dialogTask);
}

function wireScreenshots() {
  const dialog = $("#task-dialog");
  window.addEventListener("paste", (e) => {
    if (!dialog.open || state.dialogTask == null) return;
    for (const item of e.clipboardData.items) {
      if (item.type.startsWith("image/")) {
        e.preventDefault();
        uploadScreenshot(item.getAsFile()).catch((err) => alert(err.message || err));
        return;
      }
    }
  });
  dialog.addEventListener("dragover", (e) => e.preventDefault());
  dialog.addEventListener("drop", (e) => {
    e.preventDefault();
    for (const file of e.dataTransfer.files) {
      if (file.type.startsWith("image/")) {
        uploadScreenshot(file).catch((err) => alert(err.message || err));
        return;
      }
    }
  });
}

// focusTask returns keyboard focus to the given task's element in the
// active view after a re-render destroyed the dialog's invoker; the card or
// chip may be gone (hidden, capped, filtered), so the tab button is the
// landmark fallback.
function focusTask(num) {
  const onFeatures = typeof featuresVisible !== "undefined" && featuresVisible;
  const el = document.querySelector(
    (onFeatures ? "#features" : "#board") + ` [data-num="${num}"]`
  );
  if (el) {
    el.focus();
    return;
  }
  $(onFeatures ? "#tab-features" : "#tab-tasks").focus();
}

// setActiveTab keeps the tab strip's class, aria-selected, and roving
// tabindex in lockstep so styling and announced state never drift.
function setActiveTab(activeId) {
  for (const id of ["tab-tasks", "tab-features", "tab-activity", "tab-decisions"]) {
    const el = document.getElementById(id);
    const active = id === activeId;
    el.classList.toggle("active", active);
    el.setAttribute("aria-selected", String(active));
    el.tabIndex = active ? 0 : -1;
  }
}

// wireTabArrows adds the tablist pattern's Left/Right roving navigation;
// activation follows focus.
function wireTabArrows() {
  const order = ["tab-tasks", "tab-features", "tab-activity", "tab-decisions"];
  document.querySelector(".tabs").addEventListener("keydown", (e) => {
    if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
    const idx = order.indexOf(document.activeElement.id);
    if (idx < 0) return;
    e.preventDefault();
    const step = e.key === "ArrowRight" ? 1 : order.length - 1;
    const el = document.getElementById(order[(idx + step) % order.length]);
    el.focus();
    el.click();
  });
}

// focusAfterRender lands keyboard focus on the first match of selector, or
// of fallback when the target is gone -- for inline mutation controls (ship,
// unship, the add buttons) whose re-render destroyed the focused element;
// the dialog action buttons restore focus through focusTask instead.
function focusAfterRender(selector, fallback) {
  const el = document.querySelector(selector) || document.querySelector(fallback);
  if (el) el.focus();
}

// renderActions offers the lifecycle moves valid for the task's state; every
// one goes through the same API (and commits) as a drag or a CLI call.
function renderActions(t) {
  const bar = $("#dialog-actions");
  bar.replaceChildren();
  const act = (label, fn) => {
    const b = document.createElement("button");
    b.textContent = label;
    b.addEventListener("click", () => {
      $("#task-dialog").close();
      mutate(fn).then(() => focusTask(t.num));
    });
    bar.append(b);
  };
  const edit = document.createElement("button");
  edit.textContent = "edit";
  edit.addEventListener("click", () => renderEditor(state.dialogData));
  bar.append(edit);

  // Lane control: the board filters, badges, and groups by lane, so the
  // dialog can move a task between lanes too (or clear it).
  const laneSel = document.createElement("select");
  laneSel.id = "lane-select";
  laneSel.title = "move this task to a lane";
  const lanes = [...document.querySelectorAll("#lane option")]
    .map((o) => o.value)
    .filter(Boolean);
  if (t.lane && !lanes.includes(t.lane)) lanes.push(t.lane);
  laneSel.append(new Option("no lane", ""));
  for (const l of lanes) laneSel.append(new Option("lane: " + l, l));
  laneSel.append(new Option("new lane...", "__new__"));
  laneSel.value = t.lane || "";
  laneSel.addEventListener("change", () => {
    let lane = laneSel.value;
    if (lane === "__new__") {
      const entered = prompt("New lane name:");
      if (!entered || !entered.trim()) {
        laneSel.value = t.lane || "";
        return;
      }
      lane = entered.trim();
    }
    if ((t.lane || "") === lane) return;
    $("#task-dialog").close();
    mutate(() => post(`/api/projects/${state.project}/tasks/${t.num}/lane`, { lane })).then(() =>
      focusTask(t.num)
    );
  });
  bar.append(laneSel);

  const status = (s) => () => post(`/api/projects/${state.project}/tasks/${t.num}/status`, { status: s });
  if (t.deferred) {
    // A live decision is answered through its option buttons above, never
    // silently resumed past.
    if (!t.has_decision) {
      act("resume", () => post(`/api/projects/${state.project}/tasks/${t.num}/resume`));
    }
    return;
  }
  if (t.status === "pending") act("start", status("in-progress"));
  if (t.status !== "done") act("done", status("done"));
  if (t.status !== "pending") act("reopen", status("pending"));
  if (t.status !== "done") {
    act("defer", () => {
      const reason = prompt("Why is this held? (required)");
      if (!reason || !reason.trim()) return Promise.resolve();
      return post(`/api/projects/${state.project}/tasks/${t.num}/defer`, { reason: reason.trim() });
    });
  }
}

// updateDecisionsPill surfaces the CROSS-PROJECT count of unanswered
// decisions in the header: the inbox exists so something needing you is
// visible whichever project is open. Clicking it opens the decisions view
// (wired in decisions.js). The same count feeds the tab chrome.
async function updateDecisionsPill() {
  const rows = await api("/api/decisions");
  const pill = $("#decisions-pill");
  pill.hidden = rows.length === 0;
  pill.textContent = rows.length === 1 ? "1 decision" : `${rows.length} decisions`;
  pendingDecisionCount = rows.length;
  updateTabBadge(rows.length);
  updateDecisionsBanner();
}

// pendingDecisionCount caches the last cross-project decision count so view
// switches can re-evaluate the banner without refetching.
let pendingDecisionCount = 0;

// updateDecisionsBanner shows a click-through strip above the columns while
// decisions await, hidden on the decisions views themselves where it would
// only point at where the user already is. Every view switcher calls this.
function updateDecisionsBanner() {
  const banner = $("#decisions-banner");
  const onDecisions = typeof decisionsVisible !== "undefined" && decisionsVisible;
  banner.hidden = pendingDecisionCount === 0 || onDecisions;
  if (!banner.hidden) {
    banner.textContent =
      pendingDecisionCount === 1
        ? "1 decision is waiting on an answer · open the inbox"
        : `${pendingDecisionCount} decisions are waiting on an answer · open the inbox`;
  }
}

// updateTabBadge mirrors the decision count into the tab chrome -- a title
// prefix and a favicon dot -- so a backgrounded board tab still signals that
// something is waiting on a human.
function updateTabBadge(count) {
  document.title = count > 0 ? `(${count}) taskman` : "taskman";
  const link = $("#favicon");
  if (!link) return;
  const canvas = document.createElement("canvas");
  canvas.width = canvas.height = 32;
  const g = canvas.getContext("2d");
  if (!g) return;
  const styles = getComputedStyle(document.documentElement);
  g.fillStyle = styles.getPropertyValue("--accent").trim() || "#2f5a88";
  if (g.roundRect) {
    g.beginPath();
    g.roundRect(2, 2, 28, 28, 7);
    g.fill();
  } else {
    g.fillRect(2, 2, 28, 28);
  }
  g.fillStyle = "#ffffff";
  g.fillRect(8, 9, 16, 3);
  g.fillRect(8, 15, 16, 3);
  g.fillRect(8, 21, 10, 3);
  if (count > 0) {
    g.fillStyle = styles.getPropertyValue("--danger").trim() || "#d07a72";
    g.beginPath();
    g.arc(24, 8, 7, 0, Math.PI * 2);
    g.fill();
    g.strokeStyle = "#ffffff";
    g.lineWidth = 2;
    g.stroke();
  }
  link.href = canvas.toDataURL("image/png");
}

// undoLast reverts the project's newest taskman commit after showing the
// user exactly what it is; the peeked hash rides along so a concurrent
// change 409s instead of undoing something else.
function undoLast() {
  mutate(async () => {
    const peek = await api(`/api/projects/${state.project}/undo`);
    if (!confirm(`Undo "${peek.subject}"?`)) return;
    await post(`/api/projects/${state.project}/undo`, { commit: peek.commit });
  });
}

function newTask() {
  const description = prompt("New task description:");
  if (!description || !description.trim()) return;
  const lane = state.lane;
  let created = null;
  mutate(async () => {
    created = await post(`/api/projects/${state.project}/tasks`, {
      description: description.trim(),
      lane,
    });
  }).then(() => {
    if (created) focusAfterRender(`#board [data-num="${created.num}"]`, "#new-task");
  });
}

function wire() {
  $("#project").addEventListener("change", (e) => {
    state.project = e.target.value;
    localStorage.setItem("taskman.project", state.project);
    state.showAllDone = false;
    updateProjectButton();
    loadTasks().catch(showError);
  });
  $("#lane").addEventListener("change", (e) => {
    state.lane = e.target.value;
    render();
  });
  $("#show-deferred").addEventListener("change", (e) => {
    state.showDeferred = e.target.checked;
    render();
  });
  $("#swimlanes").addEventListener("change", (e) => {
    state.swimlanes = e.target.checked;
    render();
  });
  $("#dialog-close").addEventListener("click", () => {
    if (confirmDiscard()) $("#task-dialog").close();
  });
  wireLightDismiss();
  $("#new-task").addEventListener("click", newTask);
  $("#undo").addEventListener("click", undoLast);
  wireTabArrows();
}

// dialogDirty reports whether an open editor holds unsaved changes: the
// editor fields carry their loaded text as defaultValue, so view mode (no
// editor fields) is never dirty and keeps its free light dismiss.
function dialogDirty() {
  for (const id of ["edit-body", "edit-title"]) {
    const el = document.getElementById(id);
    if (el && el.value !== el.defaultValue) return true;
  }
  return false;
}

// confirmDiscard gates the accidental close paths; save and the explicit
// cancel button close without asking.
function confirmDiscard() {
  return !dialogDirty() || confirm("Discard unsaved changes?");
}

// wireLightDismiss closes the dialog on a backdrop click: content lives in
// child elements, so only backdrop clicks target the dialog itself. The
// mousedown must start on the backdrop too, or selecting text in the edit
// textarea and releasing outside would discard the edit mid-drag. Unsaved
// edits prompt before any of the three close paths (backdrop, X, Escape)
// discards them.
function wireLightDismiss() {
  const dialog = $("#task-dialog");
  let downOnBackdrop = false;
  dialog.addEventListener("mousedown", (e) => {
    downOnBackdrop = e.target === dialog;
  });
  dialog.addEventListener("click", (e) => {
    if (e.target === dialog && downOnBackdrop && confirmDiscard()) dialog.close();
  });
  dialog.addEventListener("cancel", (e) => {
    if (!confirmDiscard()) e.preventDefault();
  });
}

// --- external-change freshness: the store is multi-writer (CLI and other
// sessions commit too), so an open tab refetches when it regains focus.
// One refetch per transition (focus + visibilitychange fire together), and
// never under an open dialog -- the refresh runs on its close instead.
// Scroll, open spec panels (renderFeatures), and a focused card survive.
let refreshQueued = false;
let refreshOnDialogClose = false;

async function refreshStale() {
  if (refreshQueued) return;
  refreshQueued = true;
  setTimeout(() => {
    refreshQueued = false;
  }, 300);
  if ($("#task-dialog").open) {
    refreshOnDialogClose = true;
    return;
  }
  const active = document.activeElement;
  const num = active && active.dataset ? active.dataset.num : null;
  const y = window.scrollY;
  await loadProjects().catch(showError);
  if (!$("#picker-panel").hidden) renderPicker();
  await loadTasks().catch(showError);
  if (typeof featuresVisible !== "undefined" && featuresVisible) {
    await loadFeatures().catch(showError);
  }
  if (typeof activityVisible !== "undefined" && activityVisible) {
    await loadActivity().catch(showError);
  }
  if (typeof decisionsVisible !== "undefined" && decisionsVisible) {
    await loadDecisions().catch(showError);
  }
  window.scrollTo(0, y);
  if (num) {
    const el = document.querySelector(`[data-num="${num}"]`);
    if (el) el.focus();
  }
}

function wireRefresh() {
  window.addEventListener("focus", refreshStale);
  document.addEventListener("visibilitychange", () => {
    if (!document.hidden) refreshStale();
  });
  $("#task-dialog").addEventListener("close", () => {
    if (refreshOnDialogClose) {
      refreshOnDialogClose = false;
      refreshStale();
    }
  });
}

function showError(err) {
  const board = $("#board");
  board.replaceChildren();
  const div = document.createElement("div");
  div.className = "empty";
  div.textContent = String(err);
  board.append(div);
}

wire();
wirePicker();
wireScreenshots();
wireRefresh();
// router.js awaits this before applying any deep link.
const bootReady = loadProjects().then(loadTasks).catch(showError);
