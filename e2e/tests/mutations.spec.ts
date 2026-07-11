import { test, expect } from "@playwright/test";
import {
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
