import { test, expect, type Page } from "@playwright/test";
import {
  BASE_URL,
  PROJECT,
  TINY_PNG,
  appendFeatureBody,
  createTaskViaAPI,
  finishTask,
  gotoBoard,
  storeIsLocal,
  uniqueDesc,
} from "../helpers";

/**
 * Features view tests: tab switching, feature creation and shipping, and
 * the duplicate-creation error. Each test creates a uniquely-named feature;
 * most rely on global teardown to prune the sandbox, though a feature can now
 * be removed through the API (task 093) for tests that clean up inline.
 */

const base = `${BASE_URL}/api/projects/${PROJECT}`;

/** Switch to the features tab and wait for the view to render. */
async function gotoFeatures(page: Page): Promise<void> {
  await gotoBoard(page);
  await Promise.all([
    page.waitForResponse((r) => r.url().includes(`/api/projects/${PROJECT}/features`)),
    page.locator("#tab-features").click(),
  ]);
  await expect(page.locator("#features .features-bar")).toBeVisible();
}

/** Create a feature through the + feature prompt and return its card. */
async function createFeatureViaUI(page: Page, description: string) {
  page.once("dialog", (d) => d.accept(description));
  await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes(`/api/projects/${PROJECT}/features`) && r.request().method() === "GET"
    ),
    page.locator("#features .features-bar button", { hasText: "+ feature" }).click(),
  ]);
  const card = page.locator(".feature-card", { hasText: description });
  await expect(card).toBeVisible();
  return card;
}

test("switching tabs shows exactly one view at a time", async ({ page }) => {
  await gotoBoard(page);
  await expect(page.locator("#board")).toBeVisible();
  await expect(page.locator("#features")).toBeHidden();

  await page.locator("#tab-features").click();
  await expect(page.locator("#features")).toBeVisible();
  await expect(page.locator("#board")).toBeHidden();
  await expect(page.locator("#tab-features")).toHaveClass(/active/);

  await page.locator("#tab-tasks").click();
  await expect(page.locator("#board")).toBeVisible();
  await expect(page.locator("#features")).toBeHidden();
  await expect(page.locator("#tab-tasks")).toHaveClass(/active/);
});

test("the + feature button creates an active feature card", async ({ page }) => {
  await gotoFeatures(page);
  const desc = uniqueDesc("feature-create");
  const card = await createFeatureViaUI(page, desc);
  await expect(card.locator("h3")).toHaveText(desc);
  await expect(card.locator(".feature-slug")).toContainText(".md");
  await expect(card.locator("button", { hasText: "ship it" })).toBeVisible();

  await card.locator("details summary").click();
  await expect(card.locator(".md h1")).toHaveText(desc);
  await expect(card.locator(".md")).toContainText("Tasks:");
});

test("creating a duplicate feature surfaces a clean already-exists error", async ({ page }) => {
  await gotoFeatures(page);
  const desc = uniqueDesc("feature-dup");
  await createFeatureViaUI(page, desc);

  const messages: string[] = [];
  page.on("dialog", async (d) => {
    if (d.type() === "prompt") await d.accept(desc);
    else {
      messages.push(d.message());
      await d.dismiss();
    }
  });
  await page.locator("#features .features-bar button", { hasText: "+ feature" }).click();
  await expect.poll(() => messages.length).toBeGreaterThan(0);
  expect(messages[0]).toMatch(/^feature ".+" already exists$/);
  expect(messages[0]).not.toContain("/");
});

test("the + feature button surfaces a clean empty-slug error and adds no card", async ({ page }) => {
  // A title made only of characters that drop out of the slug (punctuation,
  // emoji, whitespace) yields an empty slug. The server refuses it with 400
  // rather than writing a feature with an empty basename that would corrupt
  // the map; the UI must alert cleanly and add nothing -- the sibling of the
  // already-exists path above.
  await gotoFeatures(page);
  const before = await page.locator(".feature-card").count();

  const messages: string[] = [];
  page.on("dialog", async (d) => {
    if (d.type() === "prompt") await d.accept("!!!");
    else {
      messages.push(d.message());
      await d.dismiss();
    }
  });
  await page.locator("#features .features-bar button", { hasText: "+ feature" }).click();
  await expect.poll(() => messages.length).toBeGreaterThan(0);
  expect(messages[0]).toContain("empty slug");
  expect(messages[0], "no store path leaked").not.toContain("/");
  // Nothing was created -- no broken card slipped into the map.
  await expect(page.locator(".feature-card")).toHaveCount(before);
});

test("ship it marks the feature shipped and renames its file", async ({ page }) => {
  await gotoFeatures(page);
  const desc = uniqueDesc("feature-ship");
  const card = await createFeatureViaUI(page, desc);

  // Shipping is now confirmed; accept the confirm() dialog.
  page.once("dialog", (d) => d.accept());
  await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes(`/api/projects/${PROJECT}/features`) && r.request().method() === "GET"
    ),
    card.locator("button", { hasText: "ship it" }).click(),
  ]);

  const shipped = page.locator(".feature-card", { hasText: desc });
  await expect(shipped.locator(".badge", { hasText: "shipped" })).toBeVisible();
  await expect(shipped.locator(".feature-slug")).toContainText(".done.md");
  await expect(shipped.locator("button", { hasText: "ship it" })).toHaveCount(0);

  const feats = await (await page.request.get(`${base}/features`)).json();
  const f = feats.find((x: { title: string }) => x.title === desc);
  expect(f?.done).toBe(true);
});

test("the features API serves slug, done, title, html, and task chips", async ({ request }) => {
  // Self-provision so this holds on a freshly-pruned sandbox (global teardown
  // removes accumulated features), not just when earlier specs left some.
  const seed = await request.post(`${base}/features`, { data: { description: uniqueDesc("api-shape") } });
  expect(seed.status()).toBe(201);

  const res = await request.get(`${base}/features`);
  expect(res.ok()).toBeTruthy();
  const feats = await res.json();
  expect(feats.length).toBeGreaterThan(0);
  for (const f of feats) {
    expect(f.slug).toMatch(/^[a-z0-9][a-z0-9-]*$/);
    expect(typeof f.done).toBe("boolean");
    expect(f.title.length).toBeGreaterThan(0);
    expect(f.html).toContain("<h1");
    expect(Array.isArray(f.tasks)).toBeTruthy();
  }
});

test("shipping an already-shipped feature 409s cleanly", async ({ request }) => {
  const desc = uniqueDesc("feature-reship");
  const created = await request.post(`${base}/features`, { data: { description: desc } });
  expect(created.status()).toBe(201);
  const { slug } = await created.json();

  expect((await request.post(`${base}/features/${slug}/done`)).ok()).toBeTruthy();
  const again = await request.post(`${base}/features/${slug}/done`);
  expect(again.status()).toBe(409);
  expect((await again.json()).error).toContain("already");
});

test("a shipped feature owns its slug: recreating it is rejected, not duplicated", async ({
  request,
}) => {
  const desc = uniqueDesc("feature-collide");
  const created = await request.post(`${base}/features`, { data: { description: desc } });
  expect(created.status()).toBe(201);
  const { slug } = await created.json();
  expect((await request.post(`${base}/features/${slug}/done`)).ok()).toBeTruthy();

  // Re-creating the same description after shipping must be refused, or a
  // later ship would os.Rename onto the shipped file and destroy its spec.
  const recreate = await request.post(`${base}/features`, { data: { description: desc } });
  expect(recreate.status()).toBe(409);
  expect((await recreate.json()).error).toContain("already exists");

  const feats = await (await request.get(`${base}/features`)).json();
  expect(feats.filter((f: { title: string }) => f.title === desc)).toHaveLength(1);
});

test("a feature body's screenshot link is rewritten through /shots/ like task detail", async ({
  request,
}) => {
  test.skip(!storeIsLocal(), "store is not local to the test runner");
  // Upload a real screenshot to a task so the link the feature embeds points
  // at a file that actually exists -- the feature then renders a live image,
  // not a dangling reference that 404s on every later features-view render
  // (features cannot be deleted, so debris would accumulate in the sandbox).
  const t = await createTaskViaAPI(request, uniqueDesc("feature-shotlink-task"));
  const upload = await request.post(`${base}/tasks/${t.num}/screenshots`, {
    multipart: { file: { name: "s.png", mimeType: "image/png", buffer: TINY_PNG } },
  });
  expect(upload.status()).toBe(201);
  const { path: shotPath } = await upload.json(); // screenshots/NNN/<name>

  const desc = uniqueDesc("feature-shotlink");
  const created = await request.post(`${base}/features`, { data: { description: desc } });
  expect(created.status()).toBe(201);
  const { slug } = await created.json();
  appendFeatureBody(slug, `\n![diag](../${shotPath})\n`);

  const feats = await (await request.get(`${base}/features`)).json();
  const html: string = feats.find((f: { slug: string }) => f.slug === slug).html;
  const served = `/shots/${PROJECT}/${shotPath.replace(/^screenshots\//, "")}`;
  expect(html).toContain(`src="${served}"`);
  expect(html).not.toContain(`src="../screenshots/`);

  // The rewritten link resolves -- the embedded image genuinely loads.
  const img = await request.get(`${BASE_URL}${served}`);
  expect(img.ok()).toBeTruthy();
  expect(img.headers()["content-type"]).toContain("image/png");

  await finishTask(request, t.num);
});

test("a spec's external links open in a new tab; relative and in-page links do not", async ({
  request,
}) => {
  test.skip(!storeIsLocal(), "store is not local to the test runner");

  const desc = uniqueDesc("feature-links");
  const created = await request.post(`${base}/features`, { data: { description: desc } });
  expect(created.status()).toBe(201);
  const { slug } = await created.json();
  appendFeatureBody(
    slug,
    `\n[ext](https://example.com/x) and bare https://autolink.test/y\n\n[rel](./local/page) and [anchor](#section)\n`
  );

  const feats = await (await request.get(`${base}/features`)).json();
  const html: string = feats.find((f: { slug: string }) => f.slug === slug).html;

  // The board is an SPA, so absolute http/https links (markdown links and GFM
  // autolinks alike) get target=_blank rel=noopener noreferrer.
  const open = `target="_blank" rel="noopener noreferrer" href=`;
  expect(html).toContain(`${open}"https://example.com/x"`);
  expect(html).toContain(`${open}"https://autolink.test/y"`);

  // Relative and in-page links stay in-tab -- rendered as bare anchors.
  expect(html).toContain(`<a href="./local/page"`);
  expect(html).toContain(`<a href="#section"`);
  expect(html).not.toContain(`${open}"./local/page"`);
  expect(html).not.toContain(`${open}"#section"`);
});

test("a spec's raw HTML is neutralized, not rendered live (no script/onerror injection)", async ({
  request,
}) => {
  test.skip(!storeIsLocal(), "store is not local to the test runner");

  // Feature specs render through the same goldmark pipeline as task bodies,
  // configured without WithUnsafe: raw HTML is dropped (replaced with an
  // "omitted" comment), never emitted as live nodes. A spec is author-supplied
  // markdown, so a live <script> or onerror handler here would be stored XSS
  // in the board SPA. This test fails loudly if someone ever enables unsafe
  // HTML rendering.
  const desc = uniqueDesc("feature-rawhtml");
  const created = await request.post(`${base}/features`, { data: { description: desc } });
  expect(created.status()).toBe(201);
  const { slug } = await created.json();
  appendFeatureBody(
    slug,
    `\n<script>window.__pwned = 1;</script>\n\ninline <b>x</b> and <img src=x onerror="window.__pwned=2"> and <a href="javascript:1">j</a>\n`
  );

  const feats = await (await request.get(`${base}/features`)).json();
  const html: string = feats.find((f: { slug: string }) => f.slug === slug).html;

  // Raw HTML was seen and deliberately dropped, not silently passed through.
  expect(html).toContain("<!-- raw HTML omitted -->");
  // None of the payload survives as executable markup.
  expect(html).not.toMatch(/<script/i);
  expect(html).not.toMatch(/onerror=/i);
  expect(html).not.toMatch(/href="javascript:/i);
});

test("a spec's GFM task list renders read-only checkboxes", async ({ page, request }) => {
  test.skip(!storeIsLocal(), "store is not local to the test runner");

  // Checklists are a common way to author a feature spec. goldmark's GFM task
  // list must render disabled checkboxes: the board never persists spec edits,
  // so a clickable checkbox would toggle to a no-op and mislead. Verify both
  // the checked/unchecked state and that every box is disabled.
  const desc = uniqueDesc("feature-tasklist");
  const created = await request.post(`${base}/features`, { data: { description: desc } });
  expect(created.status()).toBe(201);
  const { slug } = await created.json();
  appendFeatureBody(slug, `\n## Checklist\n\n- [x] shipped thing\n- [ ] pending thing\n`);

  await gotoBoard(page);
  await Promise.all([
    page.waitForResponse((r) => r.url().includes(`/api/projects/${PROJECT}/features`)),
    page.locator("#tab-features").click(),
  ]);
  const card = page.locator(".feature-card", { hasText: desc });
  await card.locator("details summary").click();

  const boxes = card.locator(".md input[type=checkbox]");
  await expect(boxes).toHaveCount(2);
  await expect(boxes.nth(0)).toBeChecked();
  await expect(boxes.nth(1)).not.toBeChecked();
  await expect(boxes.nth(0)).toBeDisabled();
  await expect(boxes.nth(1)).toBeDisabled();
});

test("the link picker links then unlinks a task, updating its chip and the rollup", async ({
  page,
  request,
}) => {
  test.skip(!storeIsLocal(), "store is not local to the test runner");

  // Feature task-linking from the UI (task 077): a per-card picker toggles a
  // task's membership on the Tasks: line via PUT, and the chip + rollup follow.
  // Before this, links could only be authored by editing the file on disk,
  // which is why real projects' feature maps stayed empty.
  const t = await createTaskViaAPI(request, uniqueDesc("uilink-task"));
  const desc = uniqueDesc("uilink-feat");
  const created = await request.post(`${base}/features`, { data: { description: desc } });
  expect(created.status()).toBe(201);
  const { slug } = await created.json();
  const pad = String(t.num).padStart(3, "0");

  await gotoBoard(page);
  await Promise.all([
    page.waitForResponse((r) => r.url().includes(`/api/projects/${PROJECT}/features`)),
    page.locator("#tab-features").click(),
  ]);
  const card = page.locator(`.feature-card[data-slug="${slug}"]`);
  await expect(card).toBeVisible();
  await expect(card.locator(".chip", { hasText: pad })).toHaveCount(0);

  // Link: open the picker, filter to the task, click its row.
  await card.locator("button.link-btn").click();
  const panel = card.locator(".link-panel");
  await expect(panel).toBeVisible();
  await panel.locator("input").fill(pad);
  await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes(`/features/${slug}/tasks`) && r.request().method() === "PUT"
    ),
    panel.locator("li", { hasText: pad }).first().click(),
  ]);
  await expect(card.locator(".chip", { hasText: pad })).toContainText("pending");
  await expect(card.locator(".rollup")).toContainText("0/1 tasks done");

  // Unlink: the row is now marked linked; clicking it removes the task.
  await card.locator("button.link-btn").click();
  const panel2 = card.locator(".link-panel");
  await panel2.locator("input").fill(pad);
  await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes(`/features/${slug}/tasks`) && r.request().method() === "PUT"
    ),
    panel2.locator("li.linked", { hasText: pad }).click(),
  ]);
  await expect(card.locator(".chip", { hasText: pad })).toHaveCount(0);
  await expect(card.locator(".rollup")).toHaveCount(0);

  await finishTask(request, t.num);
});

test("the + task button on a feature creates a task already linked to it", async ({
  page,
  request,
}) => {
  test.skip(!storeIsLocal(), "store is not local to the test runner");

  const desc = uniqueDesc("uiaddtask-feat");
  const created = await request.post(`${base}/features`, { data: { description: desc } });
  expect(created.status()).toBe(201);
  const { slug } = await created.json();

  await gotoBoard(page);
  await Promise.all([
    page.waitForResponse((r) => r.url().includes(`/api/projects/${PROJECT}/features`)),
    page.locator("#tab-features").click(),
  ]);
  const card = page.locator(`.feature-card[data-slug="${slug}"]`);
  await expect(card).toBeVisible();
  await expect(card.locator(".chip")).toHaveCount(0);

  // + task prompts for a description and creates the task already linked.
  const taskDesc = uniqueDesc("uiaddtask");
  page.once("dialog", (d) => d.accept(taskDesc));
  await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes(`/api/projects/${PROJECT}/features`) && r.request().method() === "GET"
    ),
    card.locator("button", { hasText: "+ task" }).click(),
  ]);

  await expect(card.locator(".chip")).toHaveCount(1);
  await expect(card.locator(".chip")).toContainText("pending");
  await expect(card.locator(".rollup")).toContainText("0/1 tasks done");

  // The new task really exists and is the one linked on the feature.
  const feats = await (await request.get(`${base}/features`)).json();
  const f = feats.find((x: { slug: string }) => x.slug === slug);
  expect(f.tasks).toHaveLength(1);
  const num = f.tasks[0].num as number;
  const { tasks } = await (await request.get(`${base}/tasks`)).json();
  expect(tasks.find((x: { num: number }) => x.num === num).title).toBe(taskDesc);

  await finishTask(request, num);
});

test("the link picker is keyboard-operable: filter then Enter links the task (task 087)", async ({
  page,
  request,
}) => {
  test.skip(!storeIsLocal(), "store is not local to the test runner");

  const t = await createTaskViaAPI(request, uniqueDesc("kbdlink-task"));
  const created = await request.post(`${base}/features`, {
    data: { description: uniqueDesc("kbdlink-feat") },
  });
  expect(created.status()).toBe(201);
  const { slug } = await created.json();
  const pad = String(t.num).padStart(3, "0");

  await gotoBoard(page);
  await Promise.all([
    page.waitForResponse((r) => r.url().includes(`/api/projects/${PROJECT}/features`)),
    page.locator("#tab-features").click(),
  ]);
  const card = page.locator(`.feature-card[data-slug="${slug}"]`);
  await card.locator("button.link-btn").click();

  const panel = card.locator(".link-panel");
  // Focus lands in the filter and the list announces itself as a listbox.
  await expect(panel.locator("input")).toBeFocused();
  await expect(panel.locator("ul")).toHaveAttribute("role", "listbox");

  // Filter to the task and link it with Enter -- no mouse touches the row.
  // Before task 087 this linked nothing; now it toggles the highlighted option.
  await panel.locator("input").fill(pad);
  await expect(panel.locator("li[role=option]").first()).toContainText(pad);
  await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes(`/features/${slug}/tasks`) && r.request().method() === "PUT"
    ),
    panel.locator("input").press("Enter"),
  ]);
  await expect(card.locator(".chip", { hasText: pad })).toContainText("pending");

  await finishTask(request, t.num);
});

test("a spec renders GFM tables and strikethrough", async ({ request }) => {
  test.skip(!storeIsLocal(), "store is not local to the test runner");
  // Roadmap specs phase work in tables and strike completed lines. These GFM
  // extensions render from the AST (not raw HTML), so they must survive
  // renderBody's no-WithUnsafe policy. (Task-list checkboxes are covered
  // separately above.)
  const created = await request.post(`${base}/features`, { data: { description: uniqueDesc("feature-gfm") } });
  expect(created.status()).toBe(201);
  const { slug } = await created.json();
  appendFeatureBody(slug, "\n| Phase | State |\n| --- | --- |\n| one | ~~done~~ |\n");

  const feats = await (await request.get(`${base}/features`)).json();
  const html: string = feats.find((f: { slug: string }) => f.slug === slug).html;
  expect(html).toContain("<table>");
  expect(html).toContain("<del>done</del>");

  // 093 lets specs clean up after themselves instead of accumulating.
  expect((await request.delete(`${base}/features/${slug}`)).status()).toBe(204);
});

test("creating a task against a bogus feature 404s and leaves no orphan task", async ({ request }) => {
  test.skip(!storeIsLocal(), "store is not local to the test runner");
  // The handler resolves the feature before minting a number, so a bad slug
  // must not leave an unlinked task behind.
  const before = (await (await request.get(`${base}/tasks`)).json()).tasks.length;
  const res = await request.post(`${base}/tasks`, {
    data: { description: uniqueDesc("orphan"), feature: "no-such-feature-zzz" },
  });
  expect(res.status()).toBe(404);
  const after = (await (await request.get(`${base}/tasks`)).json()).tasks.length;
  expect(after).toBe(before);
});
