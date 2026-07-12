"use strict";

// Decisions view: the answer queue as a first-class surface -- a
// cross-project inbox plus a this-project scope, each row deep-linking into
// the task dialog where the answer widget lives. Uses api()/state/showError
// globals from board.js and the tab plumbing shared with the other views.

let decisionsVisible = false;
let decisionsScope = "all"; // "all" (inbox) | "project"

async function loadDecisions() {
  const url =
    decisionsScope === "all"
      ? "/api/decisions"
      : `/api/projects/${state.project}/decisions`;
  const rows = await api(url);
  const view = $("#decisions");
  view.replaceChildren();

  const bar = document.createElement("div");
  bar.className = "decisions-bar";
  for (const [scope, label] of [["all", "all projects"], ["project", "this project"]]) {
    const b = document.createElement("button");
    b.textContent = label;
    if (scope === decisionsScope) b.classList.add("active");
    b.addEventListener("click", () => {
      decisionsScope = scope;
      loadDecisions().catch(showError);
      if (typeof writeHash === "function") writeHash();
    });
    bar.append(b);
  }
  view.append(bar);

  for (const row of rows) {
    const el = document.createElement("button");
    el.type = "button";
    el.className = "decision-row";
    const q = document.createElement("span");
    q.className = "decision-row-q";
    q.textContent = row.question;
    el.append(q);
    const where = document.createElement("span");
    where.className = "decision-row-where mono";
    where.textContent = `${row.project} ${String(row.num).padStart(3, "0")} · ${row.title}`;
    el.append(where);
    const opts = document.createElement("span");
    opts.className = "decision-row-opts";
    opts.textContent = `${row.options} options`;
    el.append(opts);
    el.addEventListener("click", () => {
      // Remember where to land after answering: the dialog opens over the
      // board, and without this the user is stranded there.
      state.decisionsReturn = location.hash;
      location.hash = `#/p/${row.project}/task/${row.num}`;
    });
    view.append(el);
  }
  if (!rows.length) {
    const empty = document.createElement("div");
    empty.className = "empty";
    empty.textContent = "no decisions awaiting you";
    view.append(empty);
  }
}

function showDecisions(scope) {
  if (scope) decisionsScope = scope;
  decisionsVisible = true;
  featuresVisible = false;
  activityVisible = false;
  $("#board").hidden = true;
  $("#features").hidden = true;
  $("#activity").hidden = true;
  $("#decisions").hidden = false;
  for (const id of ["#tab-tasks", "#tab-features", "#tab-activity"]) {
    $(id).classList.remove("active");
  }
  $("#tab-decisions").classList.add("active");
  loadDecisions().catch(showError);
}

$("#tab-decisions").addEventListener("click", () => showDecisions());
for (const id of ["#tab-tasks", "#tab-features", "#tab-activity"]) {
  $(id).addEventListener("click", () => {
    decisionsVisible = false;
    $("#decisions").hidden = true;
    $("#tab-decisions").classList.remove("active");
  });
}
$("#project").addEventListener("change", () => {
  if (decisionsVisible) loadDecisions().catch(showError);
});
// The header pill is the inbox's front door.
$("#decisions-pill").addEventListener("click", () => showDecisions("all"));
