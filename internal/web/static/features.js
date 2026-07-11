"use strict";

// Features view: the product's source of truth, one card per features/ file
// with per-linked-task status chips. Uses the api()/state globals from
// board.js.

let featuresVisible = false;

function switchTab(toFeatures) {
  featuresVisible = toFeatures;
  $("#board").hidden = toFeatures;
  $("#features").hidden = !toFeatures;
  $("#tab-tasks").classList.toggle("active", !toFeatures);
  $("#tab-features").classList.toggle("active", toFeatures);
  if (toFeatures) loadFeatures().catch(showError);
}

async function loadFeatures() {
  if (!state.project) return;
  const feats = await api(`/api/projects/${state.project}/features`);
  renderFeatures(feats);
}

// chip renders a linked task's status. Interactive chips are real buttons so
// keyboard users can reach them and Enter/Space opens the dialog; "missing"
// chips stay inert spans.
function chip(c) {
  const interactive = c.status !== "missing";
  const el = document.createElement(interactive ? "button" : "span");
  el.className = "chip " + c.status.replace("/", "-");
  el.dataset.num = c.num;
  el.textContent = `${String(c.num).padStart(3, "0")} ${c.status}`;
  if (interactive) {
    el.type = "button";
    el.addEventListener("click", () => openTask(c.num).catch((err) => alert(err.message || err)));
  }
  return el;
}

// --- task linking: a per-card picker toggles one task's membership on the
// feature's Tasks: line per open (PUT rewrites the whole list); + task
// creates a task already linked. One panel at a time.
let linkPanel = null;

function closeLinkPanel() {
  if (linkPanel) {
    linkPanel.remove();
    linkPanel = null;
  }
}

function putFeatureTasks(f, nums) {
  closeLinkPanel();
  api(`/api/projects/${state.project}/features/${f.slug}/tasks`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ tasks: nums }),
  })
    .then(loadFeatures)
    .then(() =>
      focusAfterRender(`#features [data-slug="${f.slug}"] summary`, "#tab-features")
    )
    .catch((err) => alert(err.message || err));
}

// openLinkPicker follows the project picker's combobox pattern: focus stays
// in the filter input, arrows move the highlight, Enter toggles the
// highlighted row, Escape closes; rows carry listbox/option roles so
// assistive tech announces them.
async function openLinkPicker(f, card) {
  closeLinkPanel();
  const data = await api(`/api/projects/${state.project}/tasks`);
  const linked = new Set(f.tasks.map((c) => c.num));
  const panel = document.createElement("div");
  panel.className = "link-panel";
  const search = document.createElement("input");
  search.type = "search";
  search.placeholder = "filter tasks...";
  search.setAttribute("role", "combobox");
  search.setAttribute("aria-expanded", "true");
  panel.append(search);
  const list = document.createElement("ul");
  list.setAttribute("role", "listbox");
  panel.append(list);

  let activeIdx = 0;
  let matches = [];
  const toggleNum = (num) => {
    const current = f.tasks.map((c) => c.num);
    putFeatureTasks(f, linked.has(num) ? current.filter((n) => n !== num) : [...current, num]);
  };
  const renderRows = () => {
    const q = search.value.trim().toLowerCase();
    matches = data.tasks.filter(
      (t) => !q || `${String(t.num).padStart(3, "0")} ${t.title}`.toLowerCase().includes(q)
    );
    activeIdx = Math.min(activeIdx, Math.max(0, matches.length - 1));
    list.replaceChildren();
    matches.forEach((t, i) => {
      const li = document.createElement("li");
      li.setAttribute("role", "option");
      li.setAttribute("aria-selected", String(i === activeIdx));
      li.textContent =
        (linked.has(t.num) ? "✓ " : "") + `${String(t.num).padStart(3, "0")} ${t.title}`;
      if (linked.has(t.num)) li.classList.add("linked");
      if (i === activeIdx) li.classList.add("active");
      li.addEventListener("click", () => toggleNum(t.num));
      list.append(li);
    });
    if (!matches.length) {
      const li = document.createElement("li");
      li.className = "dim";
      li.textContent = "no matching tasks";
      list.append(li);
    }
  };
  search.addEventListener("input", () => {
    activeIdx = 0;
    renderRows();
  });
  search.addEventListener("keydown", (e) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      activeIdx = Math.min(activeIdx + 1, matches.length - 1);
      renderRows();
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      activeIdx = Math.max(activeIdx - 1, 0);
      renderRows();
    } else if (e.key === "Enter") {
      e.preventDefault();
      if (matches[activeIdx]) toggleNum(matches[activeIdx].num);
    } else if (e.key === "Escape") {
      e.stopPropagation();
      closeLinkPanel();
    }
  });
  renderRows();
  card.append(panel);
  linkPanel = panel;
  search.focus();
}

document.addEventListener("click", (e) => {
  if (linkPanel && !e.target.closest(".link-panel") && !e.target.closest(".link-btn")) {
    closeLinkPanel();
  }
});

function featureCard(f, specOpen) {
  const el = document.createElement("article");
  el.className = "feature-card" + (f.done ? " done" : "");
  el.dataset.slug = f.slug;

  const head = document.createElement("div");
  head.className = "feature-head";
  const title = document.createElement("h3");
  title.textContent = f.title;
  head.append(title);
  if (f.tasks.length) {
    // Same rollup as `taskman feature list`: done over ALL linked numbers
    // (missing tasks count in the denominator there too).
    const done = f.tasks.filter((c) => c.status === "done").length;
    const rollup = document.createElement("span");
    rollup.className = "rollup";
    rollup.textContent = `${done}/${f.tasks.length} tasks done`;
    head.append(rollup);
  }
  // Mirrors mutate(): the refresh runs whether the POST succeeded or 409'd,
  // so a stale card self-corrects to server state after a lost race.
  const link = document.createElement("button");
  link.type = "button";
  link.className = "link-btn";
  link.textContent = "link";
  link.title = "link or unlink tasks";
  link.addEventListener("click", (e) => {
    e.stopPropagation();
    openLinkPicker(f, el).catch((err) => alert(err.message || err));
  });
  head.append(link);

  const addTask = document.createElement("button");
  addTask.type = "button";
  addTask.textContent = "+ task";
  addTask.title = "create a task linked to this feature";
  addTask.addEventListener("click", () => {
    const description = prompt(`New task for "${f.title}":`);
    if (!description || !description.trim()) return;
    post(`/api/projects/${state.project}/tasks`, {
      description: description.trim(),
      feature: f.slug,
    })
      .then(loadFeatures)
      .then(() =>
        focusAfterRender(`#features [data-slug="${f.slug}"] summary`, "#tab-features")
      )
      .catch((err) => alert(err.message || err));
  });
  head.append(addTask);

  const shipAction = (route) => async () => {
    try {
      await post(`/api/projects/${state.project}/features/${f.slug}/${route}`);
    } catch (err) {
      alert(err.message || err);
    }
    await loadFeatures().catch(showError);
    focusAfterRender(`#features [data-slug="${f.slug}"] summary`, "#tab-features");
  };
  if (f.done) {
    const badge = document.createElement("span");
    badge.className = "badge";
    badge.textContent = "shipped";
    head.append(badge);
    const unship = document.createElement("button");
    unship.textContent = "unship";
    unship.addEventListener("click", shipAction("reopen"));
    head.append(unship);
  } else {
    const done = document.createElement("button");
    done.textContent = "ship it";
    done.addEventListener("click", () => {
      if (!confirm(`Ship "${f.title}"?`)) return;
      shipAction("done")();
    });
    head.append(done);
  }
  el.append(head);

  const slug = document.createElement("div");
  slug.className = "feature-slug mono";
  slug.textContent = f.slug + (f.done ? ".done" : "") + ".md";
  el.append(slug);

  if (f.tasks.length) {
    const chips = document.createElement("div");
    chips.className = "chips";
    for (const c of f.tasks) chips.append(chip(c));
    el.append(chips);
  }

  const details = document.createElement("details");
  details.open = Boolean(specOpen);
  const summary = document.createElement("summary");
  summary.textContent = "spec";
  details.append(summary);
  const body = document.createElement("div");
  body.className = "md";
  body.innerHTML = f.html;
  details.append(body);
  el.append(details);

  return el;
}

// renderingFeatures suppresses the router's toggle listener during rebuilds:
// recreating an open <details> fires a toggle, and with 2+ open panels those
// rebuild toggles would rewrite the hash, whose hashchange re-renders -- a
// ping-pong loop. Toggle events are queued as tasks, so the flag clears on a
// queued task too, after every rebuild toggle has fired.
let renderingFeatures = false;

function renderFeatures(feats) {
  renderingFeatures = true;
  const view = $("#features");
  // The rebuild would discard reading position: remember which spec panels
  // are open (and the scroll offset) and restore them after.
  const openSlugs = new Set(
    [...view.querySelectorAll(".feature-card details[open]")].map(
      (d) => d.closest(".feature-card").dataset.slug
    )
  );
  const scrollY = window.scrollY;
  view.replaceChildren();

  const bar = document.createElement("div");
  bar.className = "features-bar";
  const add = document.createElement("button");
  add.textContent = "+ feature";
  add.addEventListener("click", () => {
    const description = prompt("New feature description:");
    if (!description || !description.trim()) return;
    post(`/api/projects/${state.project}/features`, { description: description.trim() })
      .then((created) =>
        loadFeatures().then(() =>
          focusAfterRender(
            `#features [data-slug="${created.slug}"] summary`,
            "#features .features-bar button"
          )
        )
      )
      .catch((err) => alert(err.message || err));
  });
  bar.append(add);
  view.append(bar);

  const active = feats.filter((f) => !f.done);
  const done = feats.filter((f) => f.done);
  for (const f of [...active, ...done]) view.append(featureCard(f, openSlugs.has(f.slug)));
  if (!feats.length) {
    const empty = document.createElement("div");
    empty.className = "empty";
    empty.textContent = "no features yet";
    view.append(empty);
  }
  window.scrollTo(0, scrollY);
  setTimeout(() => {
    renderingFeatures = false;
  }, 0);
}

$("#tab-tasks").addEventListener("click", () => switchTab(false));
$("#tab-features").addEventListener("click", () => switchTab(true));
$("#project").addEventListener("change", () => {
  if (featuresVisible) loadFeatures().catch(showError);
});
