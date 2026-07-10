import { test, expect } from "@playwright/test";
import { SEEDS, card, gotoBoard, openCard, taskByTitle } from "../helpers";

/**
 * Read-only board tests: rendering, filters, and the task detail dialog.
 * These assert against the seeded fixture tasks and never mutate the store.
 */

test.beforeEach(async ({ page }) => {
  await gotoBoard(page);
});

test("renders the three status columns with counts", async ({ page }) => {
  await expect(page.locator("header h1")).toHaveText("taskman");
  const headings = page.locator(".column h2");
  await expect(headings).toHaveText([/Pending/, /In progress/, /Done/]);
  for (const col of ["pending", "in-progress", "done"]) {
    const column = page.locator(`.column.${col}`);
    const count = Number(await column.locator(".count").textContent());
    expect(count).toBeGreaterThanOrEqual(1);
  }
});

test("places the seeded tasks in their status columns", async ({ page }) => {
  const pending = await taskByTitle(page.request, SEEDS.pendingWeb);
  const inProgress = await taskByTitle(page.request, SEEDS.inProgress);
  const done = await taskByTitle(page.request, SEEDS.done);
  await expect(page.locator(`.column.pending .card[data-num="${pending.num}"]`)).toBeVisible();
  await expect(
    page.locator(`.column.in-progress .card[data-num="${inProgress.num}"]`)
  ).toBeVisible();
  // The done column caps at 15 cards; the sandbox accumulates done tasks
  // across runs, so reveal them all before asserting the seed is present.
  const showAll = page.locator(".column.done .show-more");
  if (await showAll.count()) await showAll.click();
  await expect(page.locator(`.column.done .card[data-num="${done.num}"]`)).toBeVisible();
});

test("cards show a zero-padded number, lane badge, and title", async ({ page }) => {
  const seed = await taskByTitle(page.request, SEEDS.pendingWeb);
  const el = card(page, seed.num);
  await expect(el.locator(".num")).toHaveText(String(seed.num).padStart(3, "0"));
  await expect(el.locator(".badge.lane")).toHaveText("web");
  await expect(el).toContainText(SEEDS.pendingWeb);
});

test("lane filter hides cards from other lanes", async ({ page }) => {
  const webSeed = await taskByTitle(page.request, SEEDS.pendingWeb);
  const e2eSeed = await taskByTitle(page.request, SEEDS.pendingE2E);
  await page.locator("#lane").selectOption("web");
  await expect(card(page, webSeed.num)).toBeVisible();
  await expect(card(page, e2eSeed.num)).toHaveCount(0);
  await page.locator("#lane").selectOption("");
  await expect(card(page, e2eSeed.num)).toBeVisible();
});

test("deferred tasks are hidden until toggled on, badged, and not draggable", async ({
  page,
}) => {
  const seed = await taskByTitle(page.request, SEEDS.deferred);
  await expect(card(page, seed.num)).toHaveCount(0);
  await page.locator("#show-deferred").check();
  const el = card(page, seed.num);
  await expect(el).toBeVisible();
  await expect(el.locator(".badge.deferred")).toHaveText("deferred");
  await expect(el).toHaveAttribute("draggable", "false");
});

test("swimlanes toggle groups cards under lane headings", async ({ page }) => {
  await page.locator("#swimlanes").check();
  const heads = page.locator(".column.pending .lane-head");
  await expect(heads).not.toHaveCount(0);
  await expect(heads.filter({ hasText: "web" })).toBeVisible();
  await expect(heads.filter({ hasText: "no lane" })).toBeVisible();
});

test("clicking a card opens the detail dialog with rendered markdown", async ({ page }) => {
  const seed = await taskByTitle(page.request, SEEDS.pendingWeb);
  await openCard(page, seed.num);
  await expect(page.locator("#dialog-file")).toHaveText(seed.file);
  await expect(page.locator("#dialog-body h1")).toContainText(SEEDS.pendingWeb);
  await page.locator("#dialog-close").click();
  await expect(page.locator("#task-dialog")).toBeHidden();
});

test("the dialog offers the lifecycle actions valid for the task's state", async ({
  page,
}) => {
  const pending = await taskByTitle(page.request, SEEDS.pendingWeb);
  await openCard(page, pending.num);
  await expect(page.locator("#dialog-actions button")).toHaveText(["start", "done", "defer"]);
  await page.locator("#dialog-close").click();

  const inProgress = await taskByTitle(page.request, SEEDS.inProgress);
  await openCard(page, inProgress.num);
  await expect(page.locator("#dialog-actions button")).toHaveText(["done", "reopen", "defer"]);
  await page.locator("#dialog-close").click();

  await page.locator("#show-deferred").check();
  const deferred = await taskByTitle(page.request, SEEDS.deferred);
  await openCard(page, deferred.num);
  await expect(page.locator("#dialog-actions button")).toHaveText(["resume"]);
});
