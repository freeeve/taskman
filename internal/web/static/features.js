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
  const shipAction = (route) => () =>
    post(`/api/projects/${state.project}/features/${f.slug}/${route}`)
      .then(loadFeatures)
      .then(() =>
        focusAfterRender(`#features [data-slug="${f.slug}"] summary`, "#tab-features")
      )
      .catch((err) => alert(err.message || err));
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

function renderFeatures(feats) {
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
}

$("#tab-tasks").addEventListener("click", () => switchTab(false));
$("#tab-features").addEventListener("click", () => switchTab(true));
$("#project").addEventListener("change", () => {
  if (featuresVisible) loadFeatures().catch(showError);
});
