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
