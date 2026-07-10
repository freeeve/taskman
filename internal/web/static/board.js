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
};

async function api(path, opts) {
  const res = await fetch(path, opts);
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

async function loadProjects() {
  const projects = await api("/api/projects");
  const sel = $("#project");
  sel.replaceChildren();
  for (const p of projects) {
    const opt = document.createElement("option");
    opt.value = p.name;
    opt.textContent = `${p.name} (${p.open})`;
    sel.append(opt);
  }
  if (!projects.some((p) => p.name === state.project)) {
    state.project = projects[0] ? projects[0].name : "";
  }
  sel.value = state.project;
}

async function loadTasks() {
  if (!state.project) return;
  const data = await api(`/api/projects/${state.project}/tasks`);
  state.tasks = data.tasks;
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

function card(t) {
  const el = document.createElement("div");
  el.className = "card" + (t.deferred ? " deferred" : "");
  el.dataset.num = t.num;
  el.dataset.status = t.status;

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
  if (t.deferred) {
    const def = document.createElement("span");
    def.className = "badge deferred";
    def.textContent = "deferred";
    meta.append(def);
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
  $("#dialog-file").textContent = data.task.file;
  $("#dialog-body").innerHTML = data.html;
  $("#task-dialog").showModal();
}

function wire() {
  $("#project").addEventListener("change", (e) => {
    state.project = e.target.value;
    localStorage.setItem("taskman.project", state.project);
    state.showAllDone = false;
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
loadProjects()
  .then(loadTasks)
  .catch(showError);
