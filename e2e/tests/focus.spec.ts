import { test, expect, type Page } from "@playwright/test";
import {
  BASE_URL,
  PROJECT,
  card,
  createTaskViaAPI,
  dialogAction,
  finishTask,
  gotoBoard,
  linkTasksToFeature,
  openCard,
  storeIsLocal,
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
