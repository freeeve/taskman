import { test, expect, type Page } from "@playwright/test";
import {
  BASE_URL,
  PROJECT,
  createTaskViaAPI,
  finishTask,
  gotoBoard,
  linkTasksToFeature,
  setStatusViaAPI,
  storeIsLocal,
  uniqueDesc,
} from "../helpers";

/**
 * Feature task-chip behavior that needs a task linked to a feature. Linking
 * means editing the feature's "Tasks:" line, which the API does not expose,
 * so these specs edit the store on disk and only run when it is local to
 * the runner (the default: the :8311 server serves this same store).
 */

const base = `${BASE_URL}/api/projects/${PROJECT}`;

test.skip(() => !storeIsLocal(), "store is not local to the test runner");

/** Create a feature via the API and return its slug. */
async function createFeature(page: Page, description: string): Promise<string> {
  const res = await page.request.post(`${base}/features`, { data: { description } });
  expect(res.status()).toBe(201);
  return (await res.json()).slug;
}

/** Switch to the features tab and wait for the card to appear. */
async function openFeature(page: Page, description: string) {
  await Promise.all([
    page.waitForResponse((r) => r.url().includes(`/api/projects/${PROJECT}/features`)),
    page.locator("#tab-features").click(),
  ]);
  const card = page.locator(".feature-card", { hasText: description });
  await expect(card).toBeVisible();
  return card;
}

test("chips render the linked task's status, including in-progress/deferred", async ({ page }) => {
  const pending = await createTaskViaAPI(page.request, uniqueDesc("chip-pending"));
  const ipd = await createTaskViaAPI(page.request, uniqueDesc("chip-ipd"));
  await setStatusViaAPI(page.request, ipd.num, "in-progress");
  await page.request.post(`${base}/tasks/${ipd.num}/defer`, { data: { reason: "chip test" } });

  const desc = uniqueDesc("chip-statuses");
  const slug = await createFeature(page, desc);
  linkTasksToFeature(slug, [pending.num, ipd.num, 999999]);

  await gotoBoard(page);
  const card = await openFeature(page, desc);

  const pendingChip = card.locator(".chip", { hasText: String(pending.num).padStart(3, "0") });
  await expect(pendingChip).toHaveClass(/chip pending/);
  await expect(pendingChip).toContainText("pending");

  const ipdChip = card.locator(".chip", { hasText: String(ipd.num).padStart(3, "0") });
  await expect(ipdChip).toHaveClass(/in-progress-deferred/);
  await expect(ipdChip).toContainText("in-progress/deferred");

  // A number with no task is an inert "missing" chip: a <span>, not a
  // <button>, so clicking it can never try to open a task that isn't there.
  // A real chip is a <button> for contrast -- that's the interactivity guard.
  const missing = card.locator(".chip", { hasText: "999999" });
  await expect(missing).toHaveClass(/missing/);
  expect(await missing.evaluate((el) => el.tagName)).toBe("SPAN");
  expect(await pendingChip.evaluate((el) => el.tagName)).toBe("BUTTON");

  await page.request.post(`${base}/tasks/${ipd.num}/resume`);
  await finishTask(page.request, ipd.num);
  await finishTask(page.request, pending.num);
});

test("acting on a task from its chip updates the chip without a tab switch", async ({ page }) => {
  const t = await createTaskViaAPI(page.request, uniqueDesc("chip-refresh"));
  const desc = uniqueDesc("chip-refresh-feat");
  const slug = await createFeature(page, desc);
  linkTasksToFeature(slug, [t.num]);

  await gotoBoard(page);
  const card = await openFeature(page, desc);
  const pad = String(t.num).padStart(3, "0");
  await expect(card.locator(".chip", { hasText: pad })).toContainText("pending");

  // Open the task dialog from the chip and start the task.
  await card.locator(".chip", { hasText: pad }).click();
  await expect(page.locator("#task-dialog")).toBeVisible();
  await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes(`/api/projects/${PROJECT}/features`) && r.request().method() === "GET"
    ),
    page.locator("#dialog-actions button", { hasText: "start" }).click(),
  ]);

  // Still on the features tab, and the chip reflects the new status now --
  // no tab round-trip needed.
  await expect(page.locator("#features")).toBeVisible();
  await expect(card.locator(".chip", { hasText: pad })).toContainText("in-progress");

  await finishTask(page.request, t.num);
});

test("acting on a shared task's chip updates that chip in every feature that links it", async ({
  page,
}) => {
  const t = await createTaskViaAPI(page.request, uniqueDesc("chip-shared"));
  const descA = uniqueDesc("chip-shared-a");
  const descB = uniqueDesc("chip-shared-b");
  const slugA = await createFeature(page, descA);
  const slugB = await createFeature(page, descB);
  linkTasksToFeature(slugA, [t.num]);
  linkTasksToFeature(slugB, [t.num]);

  await gotoBoard(page);
  await openFeature(page, descA);
  const pad = String(t.num).padStart(3, "0");
  const chipA = page.locator(`.feature-card[data-slug="${slugA}"] .chip`, { hasText: pad });
  const chipB = page.locator(`.feature-card[data-slug="${slugB}"] .chip`, { hasText: pad });
  await expect(chipA).toContainText("pending");
  await expect(chipB).toContainText("pending");

  // Start the task from feature A's chip; the re-render reflects store state,
  // so feature B's chip for the same task must update too.
  await chipA.click();
  await expect(page.locator("#task-dialog")).toBeVisible();
  await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes(`/api/projects/${PROJECT}/features`) && r.request().method() === "GET"
    ),
    page.locator("#dialog-actions button", { hasText: "start" }).click(),
  ]);
  await expect(chipA).toContainText("in-progress");
  await expect(chipB).toContainText("in-progress");

  await finishTask(page.request, t.num);
});

test("acting on a chip keeps that feature's open spec panel open", async ({ page }) => {
  const t = await createTaskViaAPI(page.request, uniqueDesc("details-keep"));
  const desc = uniqueDesc("details-keep-feat");
  const slug = await createFeature(page, desc);
  linkTasksToFeature(slug, [t.num]);

  await gotoBoard(page);
  const card = await openFeature(page, desc);
  const pad = String(t.num).padStart(3, "0");

  // Expand the spec, then act on the task from its chip.
  await card.locator("details summary").click();
  await expect(card.locator("details")).toHaveJSProperty("open", true);
  await card.locator(".chip", { hasText: pad }).click();
  await expect(page.locator("#task-dialog")).toBeVisible();
  await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes(`/api/projects/${PROJECT}/features`) && r.request().method() === "GET"
    ),
    page.locator("#dialog-actions button", { hasText: "start" }).click(),
  ]);

  // The refresh updated the chip but kept the spec panel open.
  const after = page.locator(".feature-card", { hasText: desc });
  await expect(after.locator(".chip", { hasText: pad })).toContainText("in-progress");
  await expect(after.locator("details")).toHaveJSProperty("open", true);

  await finishTask(page.request, t.num);
});

test("an interactive chip is a focusable button and Enter opens the task", async ({ page }) => {
  const t = await createTaskViaAPI(page.request, uniqueDesc("chip-a11y"));
  const desc = uniqueDesc("chip-a11y-feat");
  const slug = await createFeature(page, desc);
  linkTasksToFeature(slug, [t.num, 999999]);

  await gotoBoard(page);
  const card = await openFeature(page, desc);
  const pad = String(t.num).padStart(3, "0");

  // The linked-task chip is a real button; keyboard focus + Enter opens it.
  const chip = card.locator(".chip", { hasText: pad });
  await expect(chip).toHaveJSProperty("tagName", "BUTTON");
  await chip.focus();
  await expect(chip).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.locator("#task-dialog")).toBeVisible();
  await expect(page.locator("#dialog-file")).toContainText(String(t.num).padStart(3, "0"));
  await page.locator("#dialog-close").click();

  // The missing chip stays an inert span -- not a button, not focusable.
  const missing = card.locator(".chip", { hasText: "999999" });
  await expect(missing).toHaveJSProperty("tagName", "SPAN");

  await finishTask(page.request, t.num);
});

test("marking a deferred task done clears the deferral: the chip shows plain done", async ({
  page,
}) => {
  const t = await createTaskViaAPI(page.request, uniqueDesc("chip-defer-done"));
  await setStatusViaAPI(page.request, t.num, "in-progress");
  await page.request.post(`${base}/tasks/${t.num}/defer`, { data: { reason: "chip defer-done" } });
  const desc = uniqueDesc("chip-defer-done-feat");
  const slug = await createFeature(page, desc);
  linkTasksToFeature(slug, [t.num]);

  await gotoBoard(page);
  const card = await openFeature(page, desc);
  const pad = String(t.num).padStart(3, "0");
  await expect(card.locator(".chip", { hasText: pad })).toHaveClass(/in-progress-deferred/);

  // Marking it done clears the deferral (SetStatus: acting on a task drops the
  // held mark). A deferred task's dialog offers only "resume" and its card is
  // undraggable, so the done move goes through the API. The chip must become a
  // plain "done" -- never the unstyled, unreachable-by-design "done/deferred".
  await setStatusViaAPI(page.request, t.num, "done");
  await page.locator("#tab-tasks").click();
  await Promise.all([
    page.waitForResponse((r) => r.url().includes(`/api/projects/${PROJECT}/features`)),
    page.locator("#tab-features").click(),
  ]);
  const doneChip = page.locator(`.feature-card[data-slug="${slug}"] .chip`, { hasText: pad });
  await expect(doneChip).toHaveClass(/chip done/);
  await expect(doneChip).not.toContainText("deferred");
});

test("a feature card shows the done-task rollup (missing counts in the denominator) and updates live", async ({
  page,
}) => {
  const doneTask = await createTaskViaAPI(page.request, uniqueDesc("rollup-done"));
  await setStatusViaAPI(page.request, doneTask.num, "done");
  const pendingTask = await createTaskViaAPI(page.request, uniqueDesc("rollup-pending"));
  const desc = uniqueDesc("rollup-feat");
  const slug = await createFeature(page, desc);
  // One done, one pending, one missing number: the rollup counts done over ALL
  // linked numbers, matching `taskman feature list` (missing counts too).
  linkTasksToFeature(slug, [doneTask.num, pendingTask.num, 999999]);

  await gotoBoard(page);
  const card = await openFeature(page, desc);
  await expect(card.locator(".rollup")).toHaveText("1/3 tasks done");

  // Marking the pending task done updates the rollup in place.
  const pad = String(pendingTask.num).padStart(3, "0");
  await card.locator(".chip", { hasText: pad }).click();
  await expect(page.locator("#task-dialog")).toBeVisible();
  await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes(`/api/projects/${PROJECT}/features`) && r.request().method() === "GET"
    ),
    page.locator("#dialog-actions button", { hasText: "done" }).click(),
  ]);
  await expect(page.locator(`.feature-card[data-slug="${slug}"] .rollup`)).toHaveText(
    "2/3 tasks done"
  );
});

test("a feature with no linked tasks shows no rollup", async ({ page }) => {
  const desc = uniqueDesc("rollup-empty");
  const slug = await createFeature(page, desc);

  await gotoBoard(page);
  const card = await openFeature(page, desc);
  await expect(card).toBeVisible();
  await expect(card.locator(".rollup")).toHaveCount(0);
  await expect(card.locator(".chips")).toHaveCount(0);
});
