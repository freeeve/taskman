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
 * the duplicate-creation error. Features cannot be deleted through the API,
 * so every test creates a uniquely-named feature; shipped and leftover
 * features accumulate in the sandbox like done tasks do.
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
