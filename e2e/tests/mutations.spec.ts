import { test, expect } from "@playwright/test";
import {
  card,
  createTaskViaUI,
  dialogAction,
  dragCardOnto,
  finishTask,
  getTasks,
  gotoBoard,
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
