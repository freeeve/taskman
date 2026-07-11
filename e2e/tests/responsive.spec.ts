import { test, expect, type Page } from "@playwright/test";
import { execFileSync } from "child_process";
import * as fs from "fs";
import * as path from "path";
import {
  BASE_URL,
  FEATURES_DIR,
  PROJECT,
  STORE,
  appendFeatureBody,
  createTaskViaAPI,
  gotoBoard,
  linkTasksToFeature,
  storeIsLocal,
} from "../helpers";

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

test("a feature with a long unbreakable-word title stays within narrow viewports", async ({
  page,
}) => {
  test.skip(!storeIsLocal(), "store is not local to the test runner");

  // One long token with no space or hyphen to break on (a unique numeric
  // suffix keeps the slug distinct); it stays under the 200-char slug limit.
  // Before the fix this widened the card, scrolled the page sideways, and
  // pushed the ship-it button off-screen (task 057).
  const title = "x".repeat(120) + Date.now();
  const res = await page.request.post(`${BASE_URL}/api/projects/${PROJECT}/features`, {
    data: { description: title },
  });
  expect(res.status()).toBe(201);
  const slug = (await res.json()).slug as string;

  // Link a task so the head also carries the done-task rollup (task 061): the
  // head is then title + rollup + ship-it, its busiest layout, which is what
  // must survive the narrow width -- not just a bare title + button.
  const linked = await createTaskViaAPI(page.request, `e2e rollup-long ${Date.now()}`);
  linkTasksToFeature(slug, [linked.num]);

  await gotoBoard(page);
  await Promise.all([
    page.waitForResponse((r) => r.url().includes(`/api/projects/${PROJECT}/features`)),
    page.locator("#tab-features").click(),
  ]);
  const cardSel = `.feature-card[data-slug="${slug}"]`;
  await expect(page.locator(cardSel)).toBeVisible();
  await expect(page.locator(`${cardSel} .rollup`)).toBeVisible();

  const offscreen = (sel: string) =>
    page.evaluate((s) => {
      const within = (el: Element | null) => {
        if (!el) return false;
        const r = el.getBoundingClientRect();
        return r.right <= window.innerWidth + 1 && r.left >= -1;
      };
      return {
        button: !within(document.querySelector(`${s} .feature-head button`)),
        rollup: !within(document.querySelector(`${s} .rollup`)),
      };
    }, sel);

  for (const width of NARROW) {
    await page.setViewportSize({ width, height: 900 });
    expect(await hasHorizontalOverflow(page), `@${width}: horizontal overflow`).toBe(false);
    // The ship-it button and the rollup must both stay within the viewport.
    const off = await offscreen(cardSel);
    expect(off.button, `@${width}: ship-it button off-screen`).toBe(false);
    expect(off.rollup, `@${width}: rollup off-screen`).toBe(false);
  }

  // Clean up the probe feature so the sandbox stays lean.
  fs.rmSync(path.join(FEATURES_DIR, `${slug}.md`));
  execFileSync("git", ["-C", STORE, "add", "-A", "--", `${PROJECT}/features`]);
  execFileSync("git", [
    "-C",
    STORE,
    "commit",
    "-q",
    "-m",
    `chore(${PROJECT}): clean up long-title responsive feature`,
    "--",
    `${PROJECT}/features`,
  ]);
});

test("a wide table in a feature spec scrolls in its own box, not the page", async ({ page }) => {
  test.skip(!storeIsLocal(), "store is not local to the test runner");

  const res = await page.request.post(`${BASE_URL}/api/projects/${PROJECT}/features`, {
    data: { description: `e2e wide-table ${Date.now()}` },
  });
  expect(res.status()).toBe(201);
  const slug = (await res.json()).slug as string;

  // A 15-column GFM table: its natural width exceeds a narrow card, so before
  // the fix it overflowed the page (task 059). It must now scroll inside .md.
  const cols = 15;
  const cell = (p: string) => "| " + Array.from({ length: cols }, (_, i) => `${p}${i}`).join(" | ") + " |";
  const sep = "| " + Array.from({ length: cols }, () => "---").join(" | ") + " |";
  appendFeatureBody(slug, `\n## data\n\n${cell("col")}\n${sep}\n${cell("v")}\n`);

  await gotoBoard(page);
  await Promise.all([
    page.waitForResponse((r) => r.url().includes(`/api/projects/${PROJECT}/features`)),
    page.locator("#tab-features").click(),
  ]);
  const cardSel = `.feature-card[data-slug="${slug}"]`;
  await page.locator(`${cardSel} summary`).click();
  await expect(page.locator(`${cardSel} .md table`)).toBeVisible();

  for (const width of NARROW) {
    await page.setViewportSize({ width, height: 900 });
    expect(await hasHorizontalOverflow(page), `@${width}: horizontal overflow`).toBe(false);
    // The table is capped at the container and scrolls internally: its box
    // stays within the viewport even though its content is wider.
    const contained = await page.evaluate((sel) => {
      const table = document.querySelector(`${sel} .md table`) as HTMLElement;
      const r = table.getBoundingClientRect();
      return {
        withinViewport: r.right <= window.innerWidth + 1 && r.left >= -1,
        scrolls: table.scrollWidth > table.clientWidth,
        overflowX: getComputedStyle(table).overflowX,
      };
    }, cardSel);
    expect(contained.withinViewport, `@${width}: table box overflows viewport`).toBe(true);
    expect(contained.overflowX, `@${width}: table not a scroll box`).toBe("auto");
  }

  fs.rmSync(path.join(FEATURES_DIR, `${slug}.md`));
  execFileSync("git", ["-C", STORE, "add", "-A", "--", `${PROJECT}/features`]);
  execFileSync("git", [
    "-C",
    STORE,
    "commit",
    "-q",
    "-m",
    `chore(${PROJECT}): clean up wide-table responsive feature`,
    "--",
    `${PROJECT}/features`,
  ]);
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
