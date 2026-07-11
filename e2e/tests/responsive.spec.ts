import { test, expect, type Page } from "@playwright/test";
import { gotoBoard } from "../helpers";

/**
 * Responsive layout: the shared header must wrap rather than overflow at
 * narrow widths, so the whole document never scrolls horizontally and every
 * header control stays on-screen -- on both the board and the features view
 * (task 029).
 */

const NARROW = [480, 390, 320];
const HEADER_CONTROLS = ["#project-button", "#lane", "#new-task", "#tab-tasks", "#tab-features"];

/** Document-level horizontal overflow: content wider than the viewport. */
async function hasHorizontalOverflow(page: Page): Promise<boolean> {
  return page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1);
}

/** Header controls whose box extends past either edge of the viewport. */
async function offscreenControls(page: Page, selectors: string[]): Promise<string[]> {
  return page.evaluate((sels) => {
    return sels.filter((sel) => {
      const el = document.querySelector(sel) as HTMLElement | null;
      if (!el) return false;
      const r = el.getBoundingClientRect();
      return r.right > window.innerWidth + 1 || r.left < -1;
    });
  }, selectors);
}

test("neither tab overflows horizontally and all header controls stay on-screen", async ({
  page,
}) => {
  await gotoBoard(page);
  for (const view of ["tasks", "features"] as const) {
    await page.locator(`#tab-${view}`).click();
    for (const width of NARROW) {
      await page.setViewportSize({ width, height: 900 });
      expect(await hasHorizontalOverflow(page), `${view} @${width}: horizontal overflow`).toBe(
        false
      );
      expect(
        await offscreenControls(page, HEADER_CONTROLS),
        `${view} @${width}: off-screen controls`
      ).toEqual([]);
    }
  }
});

test("the project picker panel stays within a narrow viewport when open", async ({ page }) => {
  await gotoBoard(page);
  await page.setViewportSize({ width: 320, height: 900 });
  await page.locator("#project-button").click();
  await expect(page.locator("#picker-panel")).toBeVisible();
  const fits = await page.evaluate(() => {
    const r = document.querySelector("#picker-panel")!.getBoundingClientRect();
    return r.left >= -1 && r.right <= window.innerWidth + 1;
  });
  expect(fits).toBe(true);
  expect(await hasHorizontalOverflow(page)).toBe(false);
});
