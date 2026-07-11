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

  // A number with no task is an inert "missing" chip.
  const missing = card.locator(".chip", { hasText: "999999" });
  await expect(missing).toHaveClass(/missing/);

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
