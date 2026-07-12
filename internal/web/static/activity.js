"use strict";

// Activity view: the store's audit trail (one commit per mutation) as a
// read-only list. Uses api()/state/showError globals from board.js and the
// tab plumbing shared with features.js.

let activityVisible = false;

function relTime(iso) {
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

async function loadActivity() {
  if (!state.project) return;
  const entries = await api(`/api/projects/${state.project}/activity?limit=50`);
  const view = $("#activity");
  view.replaceChildren();
  for (const e of entries) {
    const row = document.createElement("div");
    row.className = "activity-row";
    const time = document.createElement("span");
    time.className = "activity-time";
    time.textContent = relTime(e.time);
    time.title = e.time;
    row.append(time);
    const summary = document.createElement("span");
    summary.className = "activity-summary";
    summary.textContent = e.summary;
    summary.title = e.subject;
    row.append(summary);
    view.append(row);
  }
  if (!entries.length) {
    const empty = document.createElement("div");
    empty.className = "empty";
    empty.textContent = "no activity yet";
    view.append(empty);
  }
}

function showActivity() {
  activityVisible = true;
  featuresVisible = false;
  if (typeof decisionsVisible !== "undefined") decisionsVisible = false;
  $("#board").hidden = true;
  $("#features").hidden = true;
  $("#decisions").hidden = true;
  $("#activity").hidden = false;
  setActiveTab("tab-activity");
  loadActivity().catch(showError);
}

$("#tab-activity").addEventListener("click", showActivity);
for (const id of ["#tab-tasks", "#tab-features"]) {
  $(id).addEventListener("click", () => {
    activityVisible = false;
    $("#activity").hidden = true;
    $("#tab-activity").classList.remove("active");
  });
}
$("#project").addEventListener("change", () => {
  if (activityVisible) loadActivity().catch(showError);
});
