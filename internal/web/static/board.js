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
  // A mutation can start from the features view (task dialog via a chip);
  // refresh it too so chips reflect the new status without a tab switch.
  // typeof-guarded: featuresVisible lives in features.js, loaded after us.
  if (typeof featuresVisible !== "undefined" && featuresVisible) {
    await loadFeatures();
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

function pickerMatches(q) {
  const needle = q.trim().toLowerCase();
  return state.projects
    .filter((p) => p.name.includes(needle))
    .sort((a, b) => b.open - a.open || a.name.localeCompare(b.name));
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
  updateDecisionsPill();
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

function card(t) {
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
      openTask(t.num).catch((err) => alert(err.message || err));
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
    meta.append(top);
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
    meta.append(bottom);
  }
  el.append(meta);

  const title = document.createElement("div");
  title.textContent = t.title;
  el.append(title);

  el.addEventListener("click", () => openTask(t.num));
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

    let capped = false;
    if (col.status === "done" && !state.showAllDone && tasks.length > DONE_CAP) {
      tasks = tasks.slice(-DONE_CAP).reverse();
      capped = true;
    } else if (col.status === "done") {
      tasks = [...tasks].reverse();
    }
    appendCards(colEl, tasks);

    if (capped) {
      const more = document.createElement("button");
      more.className = "show-more";
      more.textContent = "show all done";
      more.addEventListener("click", () => {
        state.showAllDone = true;
        render();
      });
      colEl.append(more);
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

async function openTask(num) {
  const data = await api(`/api/projects/${state.project}/tasks/${num}`);
  state.dialogTask = num;
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
    mutate(() => post(`/api/projects/${state.project}/tasks/${t.num}/answer`, payload)).then(() =>
      focusTask(t.num)
    );
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
  titleInput.value = data.task.title;
  titleInput.title = "task title (changes the slug/filename)";
  body.append(titleInput);
  const ta = document.createElement("textarea");
  ta.id = "edit-body";
  ta.value = data.body;
  body.append(ta);

  const bar = $("#dialog-actions");
  bar.replaceChildren();
  const save = document.createElement("button");
  save.textContent = "save";
  save.addEventListener("click", async () => {
    const payload = { body: ta.value };
    const title = titleInput.value.trim();
    if (title && title !== data.task.title) payload.title = title;
    try {
      await api(`/api/projects/${state.project}/tasks/${data.task.num}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
    } catch (err) {
      alert(err.message || err);
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

// updateDecisionsPill surfaces the count of unanswered decisions in the
// header so they are findable without hunting the deferred filter; clicking
// it turns the deferred toggle on so the badged cards show.
function updateDecisionsPill() {
  const pill = $("#decisions-pill");
  const count = state.tasks.filter((t) => t.has_decision).length;
  pill.hidden = count === 0;
  pill.textContent = count === 1 ? "1 decision" : `${count} decisions`;
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
  $("#dialog-close").addEventListener("click", () => $("#task-dialog").close());
  wireLightDismiss();
  $("#new-task").addEventListener("click", newTask);
  $("#undo").addEventListener("click", undoLast);
  $("#decisions-pill").addEventListener("click", () => {
    state.showDeferred = true;
    $("#show-deferred").checked = true;
    render();
  });
}

// wireLightDismiss closes the dialog on a backdrop click: content lives in
// child elements, so only backdrop clicks target the dialog itself. The
// mousedown must start on the backdrop too, or selecting text in the edit
// textarea and releasing outside would discard the edit mid-drag.
function wireLightDismiss() {
  const dialog = $("#task-dialog");
  let downOnBackdrop = false;
  dialog.addEventListener("mousedown", (e) => {
    downOnBackdrop = e.target === dialog;
  });
  dialog.addEventListener("click", (e) => {
    if (e.target === dialog && downOnBackdrop) dialog.close();
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
  await loadTasks().catch(showError);
  if (typeof featuresVisible !== "undefined" && featuresVisible) {
    await loadFeatures().catch(showError);
  }
  if (typeof activityVisible !== "undefined" && activityVisible) {
    await loadActivity().catch(showError);
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
