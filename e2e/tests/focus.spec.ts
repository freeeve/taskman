import { test, expect, type Page } from "@playwright/test";
import { execFileSync } from "child_process";
import * as fs from "fs";
import * as path from "path";
import {
  BASE_URL,
  FEATURES_DIR,
  PROJECT,
  STORE,
  card,
  createTaskViaAPI,
  dialogAction,
  finishTask,
  gotoBoard,
  linkTasksToFeature,
  openCard,
  storeIsLocal,
  taskByTitle,
  uniqueDesc,
} from "../helpers";

/**
 * Regression for task 045: a lifecycle action from the task dialog re-renders
 * the view (replaceChildren), which destroys the element the native <dialog>
 * would return keyboard focus to, dropping focus to <body>. The fix chains
 * focusTask(num) after the refresh: focus returns to the task's card/chip when
 * it still renders, and falls back to the active tab button when it does not
 * (done column capped, deferred hidden, lane filtered). These specs mutate the
 * store, so they run against the sandbox project only.
 */

const base = `${BASE_URL}/api/projects/${PROJECT}`;

test("board: focus returns to the acted-on card after a mutation from the dialog", async ({
  page,
}) => {
  const t = await createTaskViaAPI(page.request, uniqueDesc("focus-start"));
  await gotoBoard(page);
  await openCard(page, t.num);

  // start moves the task to in-progress -- the card still renders (same
  // data-num), so focus must land back on it, not on <body>.
  await dialogAction(page, "start");
  await expect(page.locator("#task-dialog")).toBeHidden();
  await expect(card(page, t.num)).toBeFocused();

  await finishTask(page.request, t.num);
});

test("board: focus falls back to the tab button when the card no longer renders", async ({
  page,
}) => {
  const t = await createTaskViaAPI(page.request, uniqueDesc("focus-defer"));
  await gotoBoard(page);
  await openCard(page, t.num);

  // defer hides the card (deferred toggle is off by default). With no element
  // to return to, focus must land on the board's tab button, not <body>.
  page.once("dialog", (d) => d.accept("held for focus regression"));
  await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes(`${PROJECT}/tasks`) && r.request().method() === "GET"
    ),
    page.locator("#dialog-actions button", { hasText: "defer" }).click(),
  ]);
  await expect(card(page, t.num)).toHaveCount(0);
  await expect(page.locator("#tab-tasks")).toBeFocused();

  await finishTask(page.request, t.num);
});

/** Create a feature via the API and return its slug. */
async function createFeature(page: Page, description: string): Promise<string> {
  const res = await page.request.post(`${base}/features`, { data: { description } });
  expect(res.status()).toBe(201);
  return (await res.json()).slug;
}

test("features: focus returns to the acted-on chip after a mutation from the dialog", async ({
  page,
}) => {
  test.skip(!storeIsLocal(), "store is not local to the test runner");
  const t = await createTaskViaAPI(page.request, uniqueDesc("focus-chip"));
  const desc = uniqueDesc("focus-chip-feat");
  const slug = await createFeature(page, desc);
  linkTasksToFeature(slug, [t.num]);

  await gotoBoard(page);
  await Promise.all([
    page.waitForResponse((r) => r.url().includes(`${PROJECT}/features`)),
    page.locator("#tab-features").click(),
  ]);
  const feature = page.locator(".feature-card", { hasText: desc });
  await expect(feature).toBeVisible();
  const pad = String(t.num).padStart(3, "0");
  const chip = feature.locator(".chip", { hasText: pad });

  // Open the task from its chip and start it. The features view refreshes in
  // place; the chip (still in-progress, so still rendered) must regain focus.
  await chip.click();
  await expect(page.locator("#task-dialog")).toBeVisible();
  await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes(`${PROJECT}/features`) && r.request().method() === "GET"
    ),
    page.locator("#dialog-actions button", { hasText: "start" }).click(),
  ]);

  const afterChip = page
    .locator(".feature-card", { hasText: desc })
    .locator(".chip", { hasText: pad });
  await expect(afterChip).toContainText("in-progress");
  await expect(afterChip).toBeFocused();

  await finishTask(page.request, t.num);
});

/** Switch to the features tab and wait for its list to load. */
async function openFeaturesTab(page: Page): Promise<void> {
  await Promise.all([
    page.waitForResponse((r) => r.url().includes(`${PROJECT}/features`)),
    page.locator("#tab-features").click(),
  ]);
}

/**
 * Remove a feature's file (active or shipped) from the store and commit, so
 * the sandbox stays clean -- features created here cannot be deleted via the
 * API. Only valid when storeIsLocal().
 */
function removeFeatureBySlug(slug: string): void {
  for (const name of [`${slug}.done.md`, `${slug}.md`]) {
    const p = path.join(FEATURES_DIR, name);
    if (fs.existsSync(p)) fs.rmSync(p);
  }
  const dirty = execFileSync("git", ["-C", STORE, "status", "--porcelain", "--", `${PROJECT}/features`], {
    encoding: "utf8",
  });
  if (!dirty.trim()) return;
  execFileSync("git", ["-C", STORE, "add", "-A", "--", `${PROJECT}/features`]);
  execFileSync("git", [
    "-C",
    STORE,
    "commit",
    "-q",
    "-m",
    `chore(${PROJECT}): clean up focus regression feature`,
    "--",
    `${PROJECT}/features`,
  ]);
}

test("features: focus lands on the shipped card's spec after ship it", async ({ page }) => {
  test.skip(!storeIsLocal(), "store is not local to the test runner");
  const desc = uniqueDesc("focus-ship");
  const slug = await createFeature(page, desc);

  await gotoBoard(page);
  await openFeaturesTab(page);
  const cardSel = `.feature-card[data-slug="${slug}"]`;
  await expect(page.locator(cardSel)).toBeVisible();

  // Ship it re-renders the features view: the button is replaced by a
  // "shipped" badge, so focus must land on the card's spec summary, not body.
  const ship = page.locator(cardSel).locator("button", { hasText: "ship it" });
  await ship.focus();
  await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes(`${PROJECT}/features`) && r.request().method() === "GET"
    ),
    ship.click(),
  ]);
  await expect(page.locator(cardSel).locator(".badge", { hasText: "shipped" })).toBeVisible();
  await expect(page.locator(`${cardSel} summary`)).toBeFocused();

  removeFeatureBySlug(slug);
});

test("features: focus lands on the new feature's spec after + feature", async ({ page }) => {
  test.skip(!storeIsLocal(), "store is not local to the test runner");
  const desc = uniqueDesc("focus-add-feat");

  await gotoBoard(page);
  await openFeaturesTab(page);

  // + feature prompts, creates, and re-renders: focus must land on the new
  // card's spec summary.
  page.once("dialog", (d) => d.accept(desc));
  await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes(`${PROJECT}/features`) && r.request().method() === "GET"
    ),
    page.locator("#features .features-bar button", { hasText: "+ feature" }).click(),
  ]);
  const newCard = page.locator(".feature-card", { hasText: desc });
  await expect(newCard).toBeVisible();
  await expect(newCard.locator("summary")).toBeFocused();

  const slug = (await newCard.getAttribute("data-slug")) as string;
  removeFeatureBySlug(slug);
});

test("board: focus lands on the new card after + task", async ({ page }) => {
  const desc = uniqueDesc("focus-add-task");

  await gotoBoard(page);

  // + task prompts, creates, and re-renders the board: focus must land on the
  // freshly created card, not fall to body.
  page.once("dialog", (d) => d.accept(desc));
  await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes(`${PROJECT}/tasks`) && r.request().method() === "GET"
    ),
    page.locator("#new-task").click(),
  ]);
  const created = await taskByTitle(page.request, desc);
  await expect(card(page, created.num)).toBeFocused();

  await finishTask(page.request, created.num);
});
