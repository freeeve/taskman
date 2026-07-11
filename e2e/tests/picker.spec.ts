import { test, expect, type Page } from "@playwright/test";
import {
  PROJECT,
  createTaskViaAPI,
  finishTask,
  gotoBoard,
  openCard,
  selectProjectViaPicker,
  uniqueDesc,
} from "../helpers";

/**
 * Searchable project picker (task 021): open via button and ctrl+k, filter
 * as you type, busy-first ordering with idle projects dimmed, keyboard
 * navigation and selection, the close paths, and localStorage persistence.
 * The suite always has at least the sandbox plus whatever else is in the
 * store, so assertions avoid hard-coding the full project set.
 */

/** Open the picker panel via the header button. */
async function openPicker(page: Page): Promise<void> {
  await page.locator("#project-button").click();
  await expect(page.locator("#picker-panel")).toBeVisible();
}

test.beforeEach(async ({ page }) => {
  await page.goto("/");
  await expect(page.locator("#project-button")).not.toBeEmpty();
});

test("the native select is a hidden state holder behind the button", async ({ page }) => {
  await expect(page.locator("#project")).toBeHidden();
  await expect(page.locator("#project-button")).toBeVisible();
  await expect(page.locator("#project-button")).toContainText(/\(\d+\)/);
});

test("clicking the button opens the panel and focuses the search", async ({ page }) => {
  await openPicker(page);
  await expect(page.locator("#picker-search")).toBeFocused();
  await expect(page.locator("#picker-list li")).not.toHaveCount(0);
});

test("ctrl+k opens the picker and escape closes it", async ({ page }) => {
  await page.keyboard.press("Control+k");
  await expect(page.locator("#picker-panel")).toBeVisible();
  await page.locator("#picker-search").press("Escape");
  await expect(page.locator("#picker-panel")).toBeHidden();
});

test("an outside click closes the picker", async ({ page }) => {
  await openPicker(page);
  await page.locator("h1").click();
  await expect(page.locator("#picker-panel")).toBeHidden();
});

test("typing filters the list and a no-match shows an empty state", async ({ page }) => {
  await openPicker(page);
  await page.locator("#picker-search").fill(PROJECT);
  await expect(page.locator("#picker-list li")).toHaveCount(1);
  await expect(page.locator("#picker-list li")).toContainText(PROJECT);

  await page.locator("#picker-search").fill("zzz-definitely-no-such-project");
  await expect(page.locator("#picker-list li")).toHaveText(/no matching projects/);
});

test("busy projects sort above idle ones, and idle ones are dimmed", async ({ page }) => {
  await openPicker(page);
  const opens = await page.locator("#picker-list li").evaluateAll((els) =>
    els.map((el) => {
      const counts = el.querySelector(".counts")?.textContent || "";
      const m = counts.match(/(\d+)\s*open/);
      return m ? Number(m[1]) : 0;
    })
  );
  const sorted = [...opens].sort((a, b) => b - a);
  expect(opens).toEqual(sorted);

  // Every dimmed row is a 0-open project.
  const dimOpens = await page.locator("#picker-list li.dim").evaluateAll((els) =>
    els.map((el) => {
      const m = (el.querySelector(".counts")?.textContent || "").match(/(\d+)\s*open/);
      return m ? Number(m[1]) : 0;
    })
  );
  for (const n of dimOpens) expect(n).toBe(0);
});

test("arrow keys move the highlight and enter selects it", async ({ page }) => {
  await openPicker(page);
  await page.locator("#picker-search").press("ArrowDown");
  await expect(page.locator("#picker-list li.active")).toHaveCount(1);
  const active = await page.locator("#picker-list li.active span").first().textContent();

  await Promise.all([
    page.waitForResponse((r) => r.url().includes("/api/projects/") && r.url().includes("/tasks")),
    page.locator("#picker-search").press("Enter"),
  ]);
  await expect(page.locator("#picker-panel")).toBeHidden();
  await expect(page.locator("#project-button")).toContainText(active!);
  expect(await page.locator("#project").inputValue()).toBe(active);
});

test("selecting the sandbox switches the board and persists across reload", async ({ page }) => {
  await Promise.all([
    page.waitForResponse((r) => r.url().includes(`/api/projects/${PROJECT}/tasks`)),
    selectProjectViaPicker(page, PROJECT),
  ]);
  await expect(page.locator("#project-button")).toContainText(PROJECT);
  await expect(page.locator(".column")).toHaveCount(3);

  await page.reload();
  await expect(page.locator("#project-button")).toContainText(PROJECT);
  expect(await page.locator("#project").inputValue()).toBe(PROJECT);
});

test("a stale project in localStorage falls back to a real project and still loads", async ({
  page,
}) => {
  // A project persisted earlier may have since been removed from the store.
  // Init must not trust it blindly (loadProjects falls back to the first
  // project); otherwise the board would load nothing on that user's next visit.
  await page.addInitScript(() => localStorage.setItem("taskman.project", "no-such-project-zzz-999"));
  await page.reload();

  await expect(page.locator(".column")).toHaveCount(3);
  const resolved = await page.locator("#project").inputValue();
  expect(resolved, "should not keep the bogus stored project").not.toBe("no-such-project-zzz-999");
  expect(resolved.length, "should resolve to a real project").toBeGreaterThan(0);
  await expect(page.locator("#project-button")).not.toBeEmpty();

  // The features map loads for the fallback project without error.
  await Promise.all([
    page.waitForResponse((r) => /\/api\/projects\/.+\/features/.test(r.url()) && r.status() === 200),
    page.locator("#tab-features").click(),
  ]);
  await expect(page.locator("#features .features-bar")).toBeVisible();
});

test("ctrl+k is ignored while the task dialog is open, and does not leave the picker stuck open", async ({
  page,
}) => {
  const t = await createTaskViaAPI(page.request, uniqueDesc("picker-dialog"));
  await gotoBoard(page);
  await openCard(page, t.num);

  // The dialog is modal (the page behind is inert), so ctrl+k must not open
  // the picker behind the backdrop (task 070).
  await page.keyboard.press("Control+k");
  await expect(page.locator("#picker-panel")).toBeHidden();

  // Closing the dialog must not reveal a picker that ctrl+k left dangling.
  await page.locator("#dialog-close").click();
  await expect(page.locator("#task-dialog")).toBeHidden();
  await expect(page.locator("#picker-panel")).toBeHidden();

  await finishTask(page.request, t.num);
});
