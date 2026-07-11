"use strict";

// Global search: cross-project full-text over task and feature titles and
// bodies. Results navigate via the hash router's deep links, so selecting a
// hit switches project and opens the item. "/" focuses the box.

let searchTimer = null;

function hideSearchResults() {
  $("#search-results").hidden = true;
}

function searchRow(hit) {
  const row = document.createElement("div");
  row.className = "search-row";
  const where = document.createElement("span");
  where.className = "search-where mono";
  where.textContent =
    hit.kind === "task" ? `${hit.project} ${String(hit.num).padStart(3, "0")}` : `${hit.project} ${hit.slug}`;
  row.append(where);
  const text = document.createElement("span");
  text.className = "search-text";
  const title = document.createElement("strong");
  title.textContent = hit.title;
  text.append(title);
  const snip = document.createElement("span");
  snip.className = "search-snippet";
  snip.textContent = " " + hit.snippet;
  text.append(snip);
  row.append(text);
  const status = document.createElement("span");
  status.className = "search-status";
  status.textContent = hit.kind === "feature" ? `feature · ${hit.status}` : hit.status;
  row.append(status);
  row.addEventListener("click", () => {
    hideSearchResults();
    $("#search").value = "";
    location.hash =
      hit.kind === "task"
        ? `#/p/${hit.project}/task/${hit.num}`
        : `#/p/${hit.project}/feature/${hit.slug}`;
  });
  return row;
}

async function runSearch() {
  const q = $("#search").value.trim();
  const box = $("#search-results");
  if (q.length < 2) {
    hideSearchResults();
    return;
  }
  const hits = await api(`/api/search?q=${encodeURIComponent(q)}&limit=30`);
  box.replaceChildren();
  for (const hit of hits) box.append(searchRow(hit));
  if (!hits.length) {
    const none = document.createElement("div");
    none.className = "search-row dim";
    none.textContent = "no matches";
    box.append(none);
  }
  box.hidden = false;
}

$("#search").addEventListener("input", () => {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(() => runSearch().catch((err) => alert(err.message || err)), 200);
});
$("#search").addEventListener("keydown", (e) => {
  if (e.key === "Escape") {
    hideSearchResults();
    e.target.blur();
  } else if (e.key === "Enter") {
    const first = document.querySelector("#search-results .search-row:not(.dim)");
    if (first) first.click();
  }
});
document.addEventListener("click", (e) => {
  if (!e.target.closest(".searchbox")) hideSearchResults();
});
document.addEventListener("keydown", (e) => {
  if (e.key === "/" && !$("#task-dialog").open) {
    const t = e.target;
    if (t instanceof HTMLInputElement || t instanceof HTMLTextAreaElement) return;
    e.preventDefault();
    $("#search").focus();
  }
});
