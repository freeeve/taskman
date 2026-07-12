import { test, expect } from "@playwright/test";
import {
  BASE_URL,
  PROJECT,
  createTaskViaAPI,
  finishTask,
  gotoBoard,
  storeIsLocal,
  uniqueDesc,
} from "../helpers";

/**
 * Global full-text search (task 084): GET /api/search?q= is cross-project over
 * task and feature titles + bodies. The in-memory index rebuilds when the
 * store's git HEAD moves, so a just-committed mutation is searchable at once.
 * These specs create sandbox docs with distinctive tokens; they need the store
 * local so the server indexes the same store the suite writes.
 */

const base = `${BASE_URL}/api/projects/${PROJECT}`;

test.skip(() => !storeIsLocal(), "store is not local to the test runner");

/** A distinctive, collision-proof lowercase-alphanumeric token. */
function token(tag: string): string {
  return `zq${tag}${Date.now()}`;
}

test("search finds a task by a distinctive token, with its project and identity", async ({
  request,
}) => {
  const tok = token("task");
  const t = await createTaskViaAPI(request, `search probe ${tok}`);

  const hits = await (await request.get(`${BASE_URL}/api/search?q=${tok}`)).json();
  const hit = hits.find((h: { num: number }) => h.num === t.num);
  expect(hit, "the new task should be searchable at once (index tracks HEAD)").toBeTruthy();
  expect(hit.project).toBe(PROJECT);
  expect(hit.kind).toBe("task");
  expect(hit.title).toContain(tok);

  await finishTask(request, t.num);
});

test("retitling a task invalidates the old title in the index and surfaces the new one", async ({
  request,
}) => {
  // Freshness is not just addition: the index tracks git HEAD, so a retitle must
  // DROP the old title's match, not merely add the new one. A stale (append-only)
  // index would keep matching the old token.
  const oldTok = token("old");
  const newTok = token("new");
  const t = await createTaskViaAPI(request, `search ${oldTok}`);

  const hitFor = async (tok: string) =>
    ((await (await request.get(`${BASE_URL}/api/search?q=${tok}`)).json()) as { num: number }[]).some(
      (h) => h.num === t.num
    );
  expect(await hitFor(oldTok), "the old title is searchable before the retitle").toBe(true);

  const res = await request.put(`${base}/tasks/${t.num}`, { data: { title: `search ${newTok}` } });
  expect(res.ok()).toBeTruthy();

  expect(await hitFor(oldTok), "the old title no longer matches (index invalidated)").toBe(false);
  expect(await hitFor(newTok), "the new title matches at once").toBe(true);

  await finishTask(request, t.num);
});

test("search finds a feature by a body token; a non-matching query is empty", async ({
  request,
}) => {
  const tok = token("feat");
  const created = await request.post(`${base}/features`, {
    data: { description: `feature search ${tok}` },
  });
  expect(created.status()).toBe(201);
  const { slug } = await created.json();

  const hits = await (await request.get(`${BASE_URL}/api/search?q=${tok}`)).json();
  const hit = hits.find((h: { slug: string; kind: string }) => h.slug === slug && h.kind === "feature");
  expect(hit).toBeTruthy();
  expect(hit.project).toBe(PROJECT);

  // A token that appears nowhere returns no hits (empty array, not an error).
  const none = await request.get(`${BASE_URL}/api/search?q=zznever${tok}`);
  expect(none.status()).toBe(200);
  expect(await none.json()).toEqual([]);
});

test("multi-term search requires every term (AND)", async ({ request }) => {
  const a = token("alpha");
  const b = token("beta");
  const both = await createTaskViaAPI(request, `${a} ${b} together`);
  const onlyA = await createTaskViaAPI(request, `${a} by itself`);

  const hits = await (await request.get(`${BASE_URL}/api/search?q=${a}+${b}`)).json();
  const nums = hits.map((h: { num: number }) => h.num);
  expect(nums).toContain(both.num);
  expect(nums, "a doc missing one term must not match the AND query").not.toContain(onlyA.num);

  await finishTask(request, both.num);
  await finishTask(request, onlyA.num);
});

test("an empty query is a clean 400, not a whole-store dump", async ({ request }) => {
  const res = await request.get(`${BASE_URL}/api/search?q=`);
  expect(res.status()).toBe(400);
});

test("the header search box lists results as you type", async ({ page, request }) => {
  const tok = token("uibox");
  const t = await createTaskViaAPI(request, `ui search ${tok}`);

  await gotoBoard(page);
  await Promise.all([
    page.waitForResponse((r) => r.url().includes("/api/search")),
    page.locator("#search").fill(tok),
  ]);
  const row = page.locator("#search-results .search-row", { hasText: tok });
  await expect(row).toBeVisible();

  await finishTask(request, t.num);
});

test("clicking a task search result deep-links to and opens that task", async ({ page, request }) => {
  const tok = token("clicktask");
  const t = await createTaskViaAPI(request, `click ${tok}`);

  await gotoBoard(page);
  await Promise.all([
    page.waitForResponse((r) => r.url().includes("/api/search")),
    page.locator("#search").fill(tok),
  ]);
  await page.locator("#search-results .search-row", { hasText: tok }).click();

  await expect(page).toHaveURL(new RegExp(`#/p/${PROJECT}/task/${t.num}$`));
  await expect(page.locator("#task-dialog")).toBeVisible();
  await expect(page.locator("#task-dialog")).toContainText(tok);

  await finishTask(request, t.num);
});

test("clicking a feature search result deep-links to and opens that spec panel", async ({
  page,
  request,
}) => {
  const tok = token("clickfeat");
  const created = await request.post(`${base}/features`, { data: { description: `click ${tok}` } });
  expect(created.status()).toBe(201);
  const { slug } = await created.json();

  await gotoBoard(page);
  await Promise.all([
    page.waitForResponse((r) => r.url().includes("/api/search")),
    page.locator("#search").fill(tok),
  ]);
  await page.locator("#search-results .search-row", { hasText: tok }).click();

  await expect(page).toHaveURL(new RegExp(`#/p/${PROJECT}/feature/${slug}$`));
  await expect(page.locator("#tab-features")).toHaveClass(/active/);
  await expect(page.locator(`.feature-card[data-slug="${slug}"] details[open]`)).toHaveCount(1);
});

test("a cross-project search result switches project and opens the item on click", async ({
  page,
  request,
}) => {
  // Cross-project search's payoff: a hit in another project deep-links there.
  // Start on a DIFFERENT project and jump to a sandbox task -- the click must
  // switch state.project (the picker's value) and open the dialog, not merely
  // change the hash. Needs a second project in the store to start from.
  const projects = await (await request.get(`${BASE_URL}/api/projects`)).json();
  const names: string[] = (projects.projects ?? projects ?? []).map(
    (p: { name?: string } | string) => (typeof p === "string" ? p : p.name)
  );
  const other = names.find((n) => n && n !== PROJECT);
  test.skip(!other, "needs a second project in the store to start from");

  const tok = token("xproj");
  const t = await createTaskViaAPI(request, `xproj ${tok}`);
  // The index tracks HEAD but can trail a commit briefly under concurrent
  // writers; confirm searchability on the wire before driving the UI.
  await expect
    .poll(async () =>
      ((await (await request.get(`${BASE_URL}/api/search?q=${tok}`)).json()) as { num: number }[]).some(
        (h) => h.num === t.num
      )
    )
    .toBe(true);

  // Start on the other project (a read-only view), then search and click.
  await page.goto(`/#/p/${other}`);
  await expect(page.locator("#project")).toHaveValue(other!);
  await Promise.all([
    page.waitForResponse((r) => r.url().includes("/api/search")),
    page.locator("#search").fill(tok),
  ]);
  await page.locator("#search-results .search-row", { hasText: tok }).click();

  // The click crosses projects: the picker value, the hash, and the open dialog
  // all land on the sandbox task.
  await expect(page).toHaveURL(new RegExp(`#/p/${PROJECT}/task/${t.num}$`));
  await expect(page.locator("#project")).toHaveValue(PROJECT);
  await expect(page.locator("#task-dialog")).toBeVisible();
  await expect(page.locator("#task-dialog")).toContainText(tok);

  await finishTask(request, t.num);
});

/**
 * Truncation affordance (task 114): the box shows at most 30 hits and, when a
 * query matches more, appends a dim "refine your search" footer so results are
 * never dropped silently. The fix fetches SEARCH_LIMIT+1 and only shows the
 * footer on a true overflow, so an exactly-30 result set must NOT display it.
 * The footer row carries the `dim` class so Enter-selects-first skips it.
 */
test("a query matching more than 30 items shows 30 hits plus a truncation footer (task 114)", async ({
  page,
  request,
}) => {
  const tok = token("trunc");
  // 31 docs sharing one token is the minimum that overflows the 30-row cap.
  await Promise.all(
    Array.from({ length: 31 }, (_, i) => createTaskViaAPI(request, `bulk ${tok} n${i}`))
  );

  await gotoBoard(page);
  await Promise.all([
    page.waitForResponse((r) => r.url().includes("/api/search")),
    page.locator("#search").fill(tok),
  ]);

  const hitRows = page.locator("#search-results .search-row:not(.dim)");
  await expect(hitRows).toHaveCount(30);
  const footer = page.locator("#search-results .search-row.dim");
  await expect(footer).toBeVisible();
  await expect(footer).toContainText(/refine your search/i);
});

test("a query matching 30 or fewer items shows no truncation footer (task 114)", async ({
  page,
  request,
}) => {
  const tok = token("nofoot");
  const made = await Promise.all([
    createTaskViaAPI(request, `few ${tok} one`),
    createTaskViaAPI(request, `few ${tok} two`),
    createTaskViaAPI(request, `few ${tok} three`),
  ]);

  await gotoBoard(page);
  await Promise.all([
    page.waitForResponse((r) => r.url().includes("/api/search")),
    page.locator("#search").fill(tok),
  ]);

  await expect(page.locator("#search-results .search-row:not(.dim)")).toHaveCount(3);
  // The limit+1 fetch means an at-or-under-cap result set never lies with a footer.
  await expect(page.locator("#search-results .search-row.dim")).toHaveCount(0);

  for (const t of made) await finishTask(request, t.num);
});
