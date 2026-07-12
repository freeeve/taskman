import { test, expect } from "@playwright/test";
import {
  BASE_URL,
  PROJECT,
  createTaskViaAPI,
  finishTask,
  gotoBoard,
  openCard,
  setStatusViaAPI,
  storeIsLocal,
  uniqueDesc,
} from "../helpers";

/**
 * Editing a task's title/body (task 080): the dialog's edit mode saves through
 * PUT tasks/{n}. A title change renames the file's slug while keeping the
 * number, lane, and status tokens; a body change rewrites the file. Bodies
 * render through the same goldmark pipeline, so raw HTML stays neutralized.
 * These specs mutate the sandbox, so need the store local.
 */

const base = `${BASE_URL}/api/projects/${PROJECT}`;

test.skip(() => !storeIsLocal(), "store is not local to the test runner");

test("editing a task body from the dialog re-renders and persists it", async ({ page, request }) => {
  const t = await createTaskViaAPI(request, uniqueDesc("edit-body"));

  await gotoBoard(page);
  await openCard(page, t.num);
  await page.locator("#dialog-actions button", { hasText: "edit" }).click();

  const marker = `edited-${Date.now()}`;
  await page.locator("#edit-body").fill(`# heading\n\nbody with ${marker}\n`);
  await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes(`/tasks/${t.num}`) && r.request().method() === "PUT"
    ),
    page.locator("#dialog-actions button", { hasText: "save" }).click(),
  ]);

  // Back in view mode, the rendered new body is shown (the dialog sets
  // #dialog-body's innerHTML directly -- no .md wrapper, unlike feature panels).
  await expect(page.locator("#dialog-body")).toContainText(marker);
  await expect(page.locator("#dialog-body h1")).toHaveText("heading");

  // It persisted to the store.
  const detail = await (await request.get(`${base}/tasks/${t.num}`)).json();
  expect(detail.body).toContain(marker);

  await finishTask(request, t.num);
});

test("editing a task title renames its slug while preserving number and status", async ({
  request,
}) => {
  const t = await createTaskViaAPI(request, uniqueDesc("edit-title-old"));
  await setStatusViaAPI(request, t.num, "in-progress");

  const newTitle = uniqueDesc("edit-title-new");
  const res = await request.put(`${base}/tasks/${t.num}`, { data: { title: newTitle } });
  expect(res.ok()).toBeTruthy();

  const { tasks } = await (await request.get(`${base}/tasks`)).json();
  const now = tasks.find((x: { num: number }) => x.num === t.num);
  expect(now, "the task keeps its number across the retitle").toBeTruthy();
  expect(now.slug).not.toBe(t.slug);
  expect(now.status).toBe("in-progress");

  // The rendered H1 reflects the new title.
  const detail = await (await request.get(`${base}/tasks/${t.num}`)).json();
  expect(detail.html).toContain(newTitle);

  await finishTask(request, t.num);
});

test("raw HTML in an edited task body is neutralized, not rendered live", async ({ request }) => {
  const t = await createTaskViaAPI(request, uniqueDesc("edit-xss"));
  await request.put(`${base}/tasks/${t.num}`, {
    data: { body: `edited <script>window.__x=1</script> and <img src=x onerror="1">` },
  });

  const detail = await (await request.get(`${base}/tasks/${t.num}`)).json();
  expect(detail.html).toContain("<!-- raw HTML omitted -->");
  expect(detail.html).not.toMatch(/<script/i);
  expect(detail.html).not.toMatch(/onerror=/i);

  await finishTask(request, t.num);
});

test("an edit with neither title nor body is a clean 400", async ({ request }) => {
  const t = await createTaskViaAPI(request, uniqueDesc("edit-empty"));
  const res = await request.put(`${base}/tasks/${t.num}`, { data: {} });
  expect(res.status()).toBe(400);
  await finishTask(request, t.num);
});

test("a task body save carrying a stale base is refused with 409, keeping the first editor's content (task 115)", async ({
  request,
}) => {
  const t = await createTaskViaAPI(request, uniqueDesc("edit-conflict"));
  const loaded = await (await request.get(`${base}/tasks/${t.num}`)).json();
  expect(loaded.etag, "detail exposes an etag token for optimistic concurrency").toBeTruthy();

  // Editor A saves against the base it loaded -> 200.
  const a = await request.put(`${base}/tasks/${t.num}`, {
    data: { body: `${loaded.body}\n\nEDITOR-A\n`, base: loaded.etag },
  });
  expect(a.status()).toBe(200);

  // Editor B saves against the SAME, now-stale base -> 409 and writes nothing.
  const b = await request.put(`${base}/tasks/${t.num}`, {
    data: { body: `${loaded.body}\n\nEDITOR-B\n`, base: loaded.etag },
  });
  expect(b.status()).toBe(409);

  const after = await (await request.get(`${base}/tasks/${t.num}`)).json();
  expect(after.body, "editor A's content survives; the stale write was rejected").toContain("EDITOR-A");
  expect(after.body).not.toContain("EDITOR-B");

  // A base-less save still succeeds -- older clients keep last-write-wins.
  const legacy = await request.put(`${base}/tasks/${t.num}`, { data: { body: "# baseless\n" } });
  expect(legacy.status()).toBe(200);

  await finishTask(request, t.num);
});

test("saving an edit after the task changed out-of-band 409s and keeps the editor open with typed text (task 115)", async ({
  page,
  request,
}) => {
  const t = await createTaskViaAPI(request, uniqueDesc("edit-oob"));
  await gotoBoard(page);
  await openCard(page, t.num);
  await page.locator("#dialog-actions button", { hasText: "edit" }).click();

  const marker = `my-unsaved-${Date.now()}`;
  await page.locator("#edit-body").fill(`# heading\n\n${marker}\n`);

  // Another session rewrites the body out-of-band, so the open editor's base
  // (the etag it loaded) is now stale.
  const oob = await request.put(`${base}/tasks/${t.num}`, { data: { body: "# changed elsewhere\n" } });
  expect(oob.status()).toBe(200);

  // The save 409s; the client alerts and stays in the editor rather than
  // discarding the user's unsaved text. Await the dialog event directly so the
  // message is captured before asserting (a late-firing handler races the check).
  const [resp, dialog] = await Promise.all([
    page.waitForResponse((r) => r.url().includes(`/tasks/${t.num}`) && r.request().method() === "PUT"),
    page.waitForEvent("dialog"),
    page.locator("#dialog-actions button", { hasText: "save" }).click(),
  ]);
  expect(resp.status()).toBe(409);
  expect(dialog.message()).toMatch(/changed since you loaded/i);
  await dialog.accept();

  // The editor is still open and still holds the typed marker.
  await expect(page.locator("#edit-body")).toBeVisible();
  await expect(page.locator("#edit-body")).toHaveValue(new RegExp(marker));

  await finishTask(request, t.num);
});

test("retitling a task to another task's title is refused, keeping both slugs unambiguous (task 108)", async ({
  request,
}) => {
  const descA = uniqueDesc("collide-a");
  const a = await createTaskViaAPI(request, descA);
  const b = await createTaskViaAPI(request, uniqueDesc("collide-b"));

  // Retitling B to A's title would duplicate A's slug -> must be refused.
  const res = await request.put(`${base}/tasks/${b.num}`, { data: { title: descA } });
  expect(res.status()).toBe(409);

  // Both tasks keep their own slug, and A still resolves by slug.
  const tasks = (await (await request.get(`${base}/tasks`)).json()).tasks;
  expect(tasks.find((t: { num: number }) => t.num === a.num).slug).toBe(a.slug);
  expect(tasks.find((t: { num: number }) => t.num === b.num).slug).toBe(b.slug);
  const lookup = await request.get(`${base}/tasks/${a.slug}`);
  expect(lookup.ok()).toBeTruthy();
  expect((await lookup.json()).task.num).toBe(a.num);

  await finishTask(request, a.num);
  await finishTask(request, b.num);
});
