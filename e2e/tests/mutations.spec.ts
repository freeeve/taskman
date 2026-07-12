import { test, expect } from "@playwright/test";
import {
  BASE_URL,
  PROJECT,
  card,
  commitsSince,
  createTaskViaAPI,
  createTaskViaUI,
  dialogAction,
  dragCardOnto,
  finishTask,
  getTasks,
  gotoBoard,
  headCommit,
  openCard,
  setStatusViaAPI,
  taskByTitle,
  uniqueDesc,
} from "../helpers";

/**
 * Mutation tests: every path that writes to the store through the UI --
 * task creation, drag-and-drop status moves, priority reorder, and the
 * dialog lifecycle actions. Each test creates its own uniquely-named tasks
 * and drives them to done at the end, so the sandbox's pending column stays
 * clear of leftovers between runs.
 */

test.beforeEach(async ({ page }) => {
  await gotoBoard(page);
});

test("the + task button creates a pending task in the current project", async ({ page }) => {
  const desc = uniqueDesc("create");
  const t = await createTaskViaUI(page, desc);
  await expect(page.locator(`.column.pending .card[data-num="${t.num}"]`)).toBeVisible();
  expect(t.status).toBe("pending");
  await finishTask(page.request, t.num);
});

test("dragging a card to another column changes its status", async ({ page }) => {
  const t = await createTaskViaUI(page, uniqueDesc("drag-status"));
  await dragCardOnto(page, t.num, page.locator(".column.in-progress"));
  await expect(page.locator(`.column.in-progress .card[data-num="${t.num}"]`)).toBeVisible();
  expect((await taskByTitle(page.request, t.title)).status).toBe("in-progress");
  await finishTask(page.request, t.num);
});

test("dragging a pending card onto another reorders priority", async ({ page }) => {
  const first = await createTaskViaUI(page, uniqueDesc("reorder-target"));
  const second = await createTaskViaUI(page, uniqueDesc("reorder-dragged"));

  await dragCardOnto(page, second.num, card(page, first.num));

  const nums = await page
    .locator(".column.pending .card")
    .evaluateAll((els) => els.map((el) => Number((el as HTMLElement).dataset.num)));
  expect(nums.indexOf(second.num)).toBeLessThan(nums.indexOf(first.num));

  const { tasks } = await getTasks(page.request);
  const order = tasks.map((t) => t.num);
  expect(order.indexOf(second.num)).toBeLessThan(order.indexOf(first.num));

  await finishTask(page.request, first.num);
  await finishTask(page.request, second.num);
});

test("the to-top button pushes a pending task to the head of priority in one reorder commit", async ({
  page,
}) => {
  // Two pending tasks so the target starts below the head; new tasks append to
  // the bottom of the pending column.
  const a = await createTaskViaUI(page, uniqueDesc("totop-a"));
  const b = await createTaskViaUI(page, uniqueDesc("totop-b"));
  const target = b.num;

  const headNum = () =>
    page.locator(".column.pending .card").first().getAttribute("data-num");
  expect(await headNum()).not.toBe(String(target));

  const before = headCommit();
  await Promise.all([
    page.waitForResponse(
      (r) => r.url().endsWith(`/${PROJECT}/order`) && r.request().method() === "PUT"
    ),
    page.locator(`.column.pending .card[data-num="${target}"] .to-top`).click(),
  ]);

  // The target now leads the pending column and stays there across a reload.
  await expect(page.locator(".column.pending .card").first()).toHaveAttribute(
    "data-num",
    String(target)
  );
  await page.reload();
  await expect(page.locator(".column.pending .card").first()).toHaveAttribute(
    "data-num",
    String(target)
  );

  // Exactly one commit touched this project's order file: the reorder (the
  // store is multi-writer, so filter to the order file rather than count raw).
  const orderCommits = commitsSince(before).filter((c) => c.files.includes(`${PROJECT}/order`));
  expect(orderCommits).toHaveLength(1);
  expect(orderCommits[0].subject).toContain("reorder tasks");

  await finishTask(page.request, a.num);
  await finishTask(page.request, b.num);
});

test("the to-bottom button sends a pending task to the tail of priority in one reorder commit (task 095)", async ({
  page,
}) => {
  // Drag reordering is insert-before, so the very bottom is unreachable by
  // drag; the to-bottom button covers it. Create a then b -- b appends last,
  // so a starts above it; sending a to the bottom must land it past b.
  const a = await createTaskViaUI(page, uniqueDesc("tobot-a"));
  const b = await createTaskViaUI(page, uniqueDesc("tobot-b"));
  const target = a.num;

  const tailNum = () => page.locator(".column.pending .card").last().getAttribute("data-num");
  expect(await tailNum()).toBe(String(b.num));
  expect(await tailNum()).not.toBe(String(target));

  const before = headCommit();
  await Promise.all([
    page.waitForResponse(
      (r) => r.url().endsWith(`/${PROJECT}/order`) && r.request().method() === "PUT"
    ),
    page.locator(`.column.pending .card[data-num="${target}"] .to-bottom`).click(),
  ]);

  // The target now trails the pending column and stays there across a reload.
  await expect(page.locator(".column.pending .card").last()).toHaveAttribute(
    "data-num",
    String(target)
  );
  await page.reload();
  await expect(page.locator(".column.pending .card").last()).toHaveAttribute(
    "data-num",
    String(target)
  );

  const orderCommits = commitsSince(before).filter((c) => c.files.includes(`${PROJECT}/order`));
  expect(orderCommits).toHaveLength(1);
  expect(orderCommits[0].subject).toContain("reorder tasks");

  await finishTask(page.request, a.num);
  await finishTask(page.request, b.num);
});

test("the priority buttons stack vertically in a gutter without inflating the meta row (tasks 096, 097)", async ({
  page,
}) => {
  // The two priority controls read better stacked (up = top, down = bottom)
  // than side by side (096); guard the orientation. They also live in an
  // absolute right gutter so the two-high stack does not inflate the meta row
  // and push the title down (097) -- guard that the meta stays one line.
  const t = await createTaskViaUI(page, uniqueDesc("stack"));
  const cardSel = `.column.pending .card[data-num="${t.num}"]`;
  const box = async (sel: string) => page.locator(`${cardSel} ${sel}`).boundingBox();
  const top = await box(".to-top");
  const bottom = await box(".to-bottom");
  expect(top && bottom).toBeTruthy();
  // Down sits below up (stacked), not beside it.
  expect(bottom!.y).toBeGreaterThanOrEqual(top!.y + top!.height - 1);
  // Both share the same horizontal column (centers aligned within a pixel or two).
  const cx = (b: { x: number; width: number }) => b.x + b.width / 2;
  expect(Math.abs(cx(top!) - cx(bottom!))).toBeLessThan(3);

  // The stack must not inflate the meta row: it is a single line (~16px), so
  // the title stays put rather than being shoved down by two button heights.
  const meta = await box(".meta");
  expect(meta!.height).toBeLessThan(24);
  // The controls are out of normal flow (absolute), which is what keeps the
  // meta row from growing to fit them.
  const position = await page.locator(`${cardSel} .priority-controls`).evaluate(
    (el) => getComputedStyle(el).position
  );
  expect(position).toBe("absolute");

  await finishTask(page.request, t.num);
});

test("show all done toggles the done column between the cap and every card (task 098)", async ({
  page,
  request,
}) => {
  // The done column caps at 15 most-recent cards; the toggle must expand to all
  // and collapse back, in-UI, without a reload. Seed >15 done tasks so the cap
  // engages regardless of what else is in the column.
  test.setTimeout(60_000);
  const DONE_CAP = 15;
  for (let i = 0; i < DONE_CAP + 1; i++) {
    const t = await createTaskViaAPI(request, uniqueDesc(`donecap-${i}`));
    await setStatusViaAPI(request, t.num, "done");
  }

  await gotoBoard(page);
  const doneCol = page.locator(".column[data-status=done]");
  const cards = doneCol.locator(".card");
  const toggle = doneCol.locator(".show-more");

  // Capped: exactly DONE_CAP cards and an expand affordance.
  await expect(cards).toHaveCount(DONE_CAP);
  await expect(toggle).toHaveText("show all done");

  // Expand: more than the cap, and the label flips to collapse.
  await toggle.click();
  await expect(toggle).toHaveText("show fewer");
  expect(await cards.count()).toBeGreaterThan(DONE_CAP);

  // Collapse: back to the cap, in-UI, no reload.
  await toggle.click();
  await expect(cards).toHaveCount(DONE_CAP);
  await expect(toggle).toHaveText("show all done");
});

test("PUT order drops nonexistent task numbers and dedupes (task 110)", async ({ request }) => {
  const a = await createTaskViaAPI(request, uniqueDesc("order-a"));
  const b = await createTaskViaAPI(request, uniqueDesc("order-b"));
  // Include the current order so the seeds survive; add a bogus number and a
  // duplicate up front to exercise the validation.
  const { order: before } = await getTasks(request);
  const res = await request.put(`${BASE_URL}/api/projects/${PROJECT}/order`, {
    data: { order: [a.num, 999999, b.num, a.num, ...before] },
  });
  expect(res.status()).toBe(204);

  const { order } = await getTasks(request);
  expect(order, "a nonexistent number is dropped").not.toContain(999999);
  expect(order.filter((n) => n === a.num), "duplicates collapse").toHaveLength(1);
  // Valid numbers keep the given relative sequence (a before b).
  expect(order.indexOf(a.num)).toBeLessThan(order.indexOf(b.num));

  // Finishing drops them from order, restoring the seed order.
  await finishTask(request, a.num);
  await finishTask(request, b.num);
});

test("the board refetches on focus, surfacing an out-of-band task without a reload", async ({
  page,
}) => {
  // Create a task through the API (a separate client). The open board is a
  // snapshot -- the store is multi-writer (CLI, other sessions) -- so it does
  // not yet show the new task.
  const t = await createTaskViaAPI(page.request, uniqueDesc("focus-refresh"));
  await expect(page.locator(`.card[data-num="${t.num}"]`)).toHaveCount(0);

  // Regaining window focus triggers a refetch; the card appears with no reload.
  await Promise.all([
    page.waitForResponse((r) => r.url().includes(`/api/projects/${PROJECT}/tasks`)),
    page.evaluate(() => window.dispatchEvent(new Event("focus"))),
  ]);
  await expect(page.locator(`.column.pending .card[data-num="${t.num}"]`)).toBeVisible();

  await finishTask(page.request, t.num);
});

test("dialog actions walk a task through start, done, and reopen", async ({ page }) => {
  const t = await createTaskViaUI(page, uniqueDesc("lifecycle"));

  await openCard(page, t.num);
  await dialogAction(page, "start");
  await expect(page.locator(`.column.in-progress .card[data-num="${t.num}"]`)).toBeVisible();

  await openCard(page, t.num);
  await dialogAction(page, "done");
  await expect(page.locator(`.column.done .card[data-num="${t.num}"]`)).toBeVisible();

  await openCard(page, t.num);
  await dialogAction(page, "reopen");
  await expect(page.locator(`.column.pending .card[data-num="${t.num}"]`)).toBeVisible();

  await finishTask(page.request, t.num);
});

test("defer requires a reason, hides the card, and resume restores it", async ({ page }) => {
  const t = await createTaskViaUI(page, uniqueDesc("defer"));

  await openCard(page, t.num);
  page.once("dialog", (d) => d.accept("waiting on an e2e decision"));
  await dialogAction(page, "defer");
  await expect(card(page, t.num)).toHaveCount(0);

  await page.locator("#show-deferred").check();
  await expect(card(page, t.num).locator(".badge.deferred")).toBeVisible();

  await openCard(page, t.num);
  await dialogAction(page, "resume");
  const el = card(page, t.num);
  await expect(el).toBeVisible();
  await expect(el.locator(".badge.deferred")).toHaveCount(0);
  expect((await taskByTitle(page.request, t.title)).deferred).toBe(false);

  await finishTask(page.request, t.num);
});

test("resume restores a deferred task's underlying status rather than forcing pending (orthogonal marker)", async ({
  request,
}) => {
  const base = `${BASE_URL}/api/projects/${PROJECT}`;
  const find = async (num: number) =>
    (await getTasks(request)).tasks.find((x: { num: number }) => x.num === num);

  const t = await createTaskViaAPI(request, uniqueDesc("defer-inprogress"));
  await setStatusViaAPI(request, t.num, "in-progress");

  // Defer is orthogonal: it flags the task without changing its status.
  expect((await request.post(`${base}/tasks/${t.num}/defer`, {
    data: { reason: "held on an external decision" },
  })).status()).toBe(200);
  const deferred = await find(t.num);
  expect(deferred.status, "defer keeps the underlying status").toBe("in-progress");
  expect(deferred.deferred).toBe(true);

  // Resume clears the marker and leaves it in-progress -- not demoted to pending.
  expect((await request.post(`${base}/tasks/${t.num}/resume`, { data: {} })).status()).toBe(200);
  const resumed = await find(t.num);
  expect(resumed.status, "resume preserves the pre-defer status").toBe("in-progress");
  expect(resumed.deferred).toBe(false);

  await finishTask(request, t.num);
});
