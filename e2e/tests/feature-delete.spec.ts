import { test, expect } from "@playwright/test";
import {
  BASE_URL,
  PROJECT,
  commitsSince,
  createTaskViaAPI,
  finishTask,
  gotoBoard,
  headCommit,
  storeIsLocal,
  uniqueDesc,
} from "../helpers";

/**
 * Feature delete / discard (task 093): a feature can be removed via
 * `DELETE /api/projects/{p}/features/{slug}`, which deletes only the spec file
 * (linked tasks survive), lands one scoped `remove feature` commit, 404s an
 * unknown slug, and stays undoable. The web card carries a confirm-guarded
 * "discard" button. These specs mutate the sandbox, so they need the store
 * local to the runner.
 */

const base = `${BASE_URL}/api/projects/${PROJECT}`;

test.skip(() => !storeIsLocal(), "store is not local to the test runner");

test("DELETE removes the feature from the list and lands one scoped remove commit", async ({
  request,
}) => {
  const desc = uniqueDesc("del-feature");
  const created = await request.post(`${base}/features`, { data: { description: desc } });
  expect(created.status()).toBe(201);
  const { slug } = await created.json();

  const before = headCommit();
  const res = await request.delete(`${base}/features/${slug}`);
  expect(res.status()).toBe(204);

  const feats = await (await request.get(`${base}/features`)).json();
  expect(feats.find((f: { slug: string }) => f.slug === slug)).toBeUndefined();

  const mine = commitsSince(before).filter((c) => c.files.some((f) => f.startsWith(`${PROJECT}/`)));
  expect(mine.length).toBeGreaterThanOrEqual(1);
  expect(mine[0].subject).toContain(`remove feature ${slug}`);
});

test("deleting a feature leaves its linked task untouched", async ({ request }) => {
  const t = await createTaskViaAPI(request, uniqueDesc("del-keeps-task"));
  const created = await request.post(`${base}/features`, {
    data: { description: uniqueDesc("del-with-link") },
  });
  const { slug } = await created.json();
  // Link the task to the feature via the API, then delete the feature.
  expect((await request.put(`${base}/features/${slug}/tasks`, { data: { tasks: [t.num] } })).ok()).toBeTruthy();
  expect((await request.delete(`${base}/features/${slug}`)).status()).toBe(204);

  // The task still exists, unchanged in status.
  const task = (await (await request.get(`${base}/tasks`)).json()).tasks.find(
    (x: { num: number }) => x.num === t.num
  );
  expect(task).toBeTruthy();
  expect(task.status).toBe("pending");

  await finishTask(request, t.num);
});

test("DELETE 404s an unknown feature slug", async ({ request }) => {
  const res = await request.delete(`${base}/features/no-such-feature-xyz`);
  expect(res.status()).toBe(404);
  expect((await res.json()).error).toBeTruthy();
});

test("undo restores a just-deleted feature", async ({ request }) => {
  const created = await request.post(`${base}/features`, {
    data: { description: uniqueDesc("del-undo") },
  });
  const { slug } = await created.json();
  expect((await request.delete(`${base}/features/${slug}`)).status()).toBe(204);

  // The removal is a single commit, so the project undo restores the file.
  const peek = await (await request.get(`${base}/undo`)).json();
  expect((await request.post(`${base}/undo`, { data: { commit: peek.commit } })).ok()).toBeTruthy();

  const feats = await (await request.get(`${base}/features`)).json();
  expect(feats.find((f: { slug: string }) => f.slug === slug)).toBeTruthy();

  // Clean up the restored feature.
  await request.delete(`${base}/features/${slug}`);
});

test("the web discard button removes the feature card after a confirm", async ({ page }) => {
  const created = await page.request.post(`${base}/features`, {
    data: { description: uniqueDesc("del-ui") },
  });
  const { slug } = await created.json();

  await gotoBoard(page);
  await page.locator("#tab-features").click();
  const cardSel = `#features [data-slug="${slug}"]`;
  await expect(page.locator(cardSel)).toBeVisible();

  page.once("dialog", (d) => d.accept());
  await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes(`/features/${slug}`) && r.request().method() === "DELETE"
    ),
    page.locator(`${cardSel} .discard-btn`).click(),
  ]);

  await expect(page.locator(cardSel)).toHaveCount(0);
});
