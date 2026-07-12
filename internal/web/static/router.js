"use strict";

// Hash router: #/p/<project>[/features | /activity | /task/<n> |
// /feature/<slug>] makes projects, views, tasks, and feature specs
// shareable and bookmarkable. Loaded last: it drives the globals the other
// scripts define, and no server route is involved.

let applyingHash = false;

function parseHash() {
  if (location.hash === "#/decisions") return { inbox: true };
  const m = location.hash.match(
    /^#\/p\/([a-z0-9][a-z0-9-]*)(?:\/(features|activity|decisions)|\/task\/(\d+)|\/feature\/([a-z0-9][a-z0-9-]*))?$/
  );
  if (!m) return null;
  return { project: m[1], view: m[2] || "tasks", task: m[3] ? Number(m[3]) : null, feature: m[4] || null };
}

// currentHash mirrors the live view; feature-panel state is written by the
// toggle listener below rather than derived (several panels may be open).
// The open dialog is checked first: it is a modal over whichever tab is
// behind it, so its URL wins -- a task opened from a feature chip must be as
// deep-linkable as one opened from a board card, and closing falls back to
// the tab's hash via the dialog's close listener.
function currentHash() {
  if (typeof decisionsVisible !== "undefined" && decisionsVisible &&
      !$("#task-dialog").open) {
    if (decisionsScope === "all" || !state.project) return "#/decisions";
    return `#/p/${state.project}/decisions`;
  }
  if (!state.project) return "";
  const base = `#/p/${state.project}`;
  if ($("#task-dialog").open && state.dialogTask != null) return `${base}/task/${state.dialogTask}`;
  if (typeof activityVisible !== "undefined" && activityVisible) return base + "/activity";
  if (typeof featuresVisible !== "undefined" && featuresVisible) return base + "/features";
  return base;
}

function writeHash(explicit) {
  if (applyingHash) return;
  const h = explicit || currentHash();
  if (h && location.hash !== h) location.hash = h;
}

// applyHash re-drives the UI from the hash: bad projects, task numbers, and
// feature slugs fall back to the nearest valid view, never an error state.
async function applyHash() {
  const h = parseHash();
  if (!h) return;
  applyingHash = true;
  try {
    if (h.inbox) {
      if ($("#task-dialog").open) $("#task-dialog").close();
      showDecisions("all");
      return;
    }
    if (h.project !== state.project && state.projects.some((p) => p.name === h.project)) {
      state.project = h.project;
      localStorage.setItem("taskman.project", state.project);
      $("#project").value = state.project;
      updateProjectButton();
      state.showAllDone = false;
      await loadTasks().catch(showError);
    }
    if ($("#task-dialog").open && !h.task) $("#task-dialog").close();
    if (h.view === "decisions") {
      showDecisions("project");
    } else if (h.view === "activity") {
      showActivity();
    } else if (h.view === "features" || h.feature) {
      switchTab(true);
      if (h.feature) {
        await loadFeatures().catch(showError);
        const card = document.querySelector(`#features [data-slug="${h.feature}"]`);
        if (card) {
          const details = card.querySelector("details");
          if (details) details.open = true;
          card.scrollIntoView({ block: "start" });
        }
      }
    } else {
      switchTab(false);
      if (h.task) await openTask(h.task).catch(() => {});
    }
  } finally {
    applyingHash = false;
  }
}

// The initial hash's project wins over localStorage; loadProjects' existing
// fallback still guards a bogus name. This runs before the bootstrap fetch
// resolves, so the first load already targets the right project.
{
  const initial = parseHash();
  if (initial) state.project = initial.project;
}

bootReady.then(applyHash);
window.addEventListener("hashchange", applyHash);

// Keep the hash in sync with in-app navigation.
{
  const baseOpenTask = openTask;
  openTask = async function (num) {
    await baseOpenTask(num);
    writeHash();
  };
}
$("#task-dialog").addEventListener("close", () => writeHash());
$("#project").addEventListener("change", () => writeHash());
for (const id of ["#tab-tasks", "#tab-features", "#tab-activity", "#tab-decisions"]) {
  $(id).addEventListener("click", () => writeHash());
}
$("#decisions-pill").addEventListener("click", () => writeHash());
// Spec panel toggles: non-bubbling, so capture. Only genuine user toggles
// win the hash -- rebuild-fired toggles (renderFeatures recreating open
// panels) are suppressed via renderingFeatures, or they feedback-loop with
// applyHash's re-render.
document.addEventListener(
  "toggle",
  (e) => {
    const card = e.target.closest ? e.target.closest(".feature-card") : null;
    if (!card || applyingHash) return;
    if (typeof renderingFeatures !== "undefined" && renderingFeatures) return;
    if (e.target.open) {
      writeHash(`#/p/${state.project}/feature/${card.dataset.slug}`);
    } else if (location.hash.endsWith(`/feature/${card.dataset.slug}`)) {
      writeHash(`#/p/${state.project}/features`);
    }
  },
  true
);
