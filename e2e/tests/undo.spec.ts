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
 * Undo (task 078): the #undo button reverts the project's newest taskman commit
 * as its own revert commit, after a GET-peek + confirm, and refuses with 409 if
 * the project moved since the peek. Undo resolves the target via the project's
 * last commit, so a concurrent change to another project is never touched.
 * These specs mutate the sandbox, so they need the store local to the runner.
 */

const base = `${BASE_URL}/api/projects/${PROJECT}`;

test.skip(() => !storeIsLocal(), "store is not local to the test runner");

test("the undo button reverts the last mutation (a feature ship) as its own revert commit", async ({
  page,
  request,
}) => {
  // Ship a feature via the API so the project's newest commit is that ship.
  const desc = uniqueDesc("undo-ship");
  const created = await request.post(`${base}/features`, { data: { description: desc } });
  expect(created.status()).toBe(201);
  const { slug } = await created.json();
  expect((await request.post(`${base}/features/${slug}/done`)).ok()).toBeTruthy();

  await gotoBoard(page);
  const before = headCommit();

  // The undo button peeks (GET), confirms, then POSTs the revert.
  page.once("dialog", (d) => d.accept());
  await Promise.all([
    page.waitForResponse((r) => r.url().includes("/undo") && r.request().method() === "POST"),
    page.locator("#undo").click(),
  ]);

  // The ship was reverted: the feature is active again.
  const feats = await (await request.get(`${base}/features`)).json();
  expect(feats.find((f: { slug: string }) => f.slug === slug).done).toBe(false);

  // The revert landed as its own commit, scoped to this project, and is itself
  // a Revert of a taskman mutation (so it stays undoable / redoable).
  const mine = commitsSince(before).filter((c) => c.files.some((f) => f.startsWith(`${PROJECT}/`)));
  expect(mine.length).toBeGreaterThanOrEqual(1);
  expect(mine[0].subject).toMatch(/^Revert "chore\(e2e-sandbox\):/);
});

test("undo 409s when the project moved since the peek", async ({ request }) => {
  // Peek the undo target, then move the project so that peeked commit is no
  // longer newest; undoing with the stale hash must refuse rather than revert
  // something the user did not confirm (the store is multi-writer).
  const t = await createTaskViaAPI(request, uniqueDesc("undo-stale"));
  const peek = await (await request.get(`${base}/undo`)).json();
  expect(peek.commit).toBeTruthy();

  const t2 = await createTaskViaAPI(request, uniqueDesc("undo-stale2"));

  const res = await request.post(`${base}/undo`, { data: { commit: peek.commit } });
  expect(res.status()).toBe(409);

  await finishTask(request, t.num);
  await finishTask(request, t2.num);
});
