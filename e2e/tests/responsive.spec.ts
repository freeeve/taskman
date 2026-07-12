import { test, expect, type Page } from "@playwright/test";
import * as fs from "fs";
import * as path from "path";
import {
  BASE_URL,
  FEATURES_DIR,
  PROJECT,
  appendFeatureBody,
  commitStore,
  createTaskViaAPI,
  finishTask,
  gotoBoard,
  linkTasksToFeature,
  storeIsLocal,
  uniqueDesc,
} from "../helpers";

/**
 * Responsive layout: the shared header must wrap rather than overflow at
 * narrow widths, so the whole document never scrolls horizontally and every
 * header control stays on-screen -- on both the board and the features view
 * (task 029).
 */

const NARROW = [480, 390, 320];
// Every interactive header control must stay on-screen when the bar wraps --
// including the ones added after this test first landed (activity tab, global
// search box, undo), so a future addition that overflows is caught.
const HEADER_CONTROLS = [
  "#project-button",
  "#lane",
  "#new-task",
  "#tab-tasks",
  "#tab-features",
  "#tab-activity",
  "#search",
  "#undo",
  "#show-deferred",
  "#swimlanes",
];

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
  commitStore(`${PROJECT}/features`, `chore(${PROJECT}): clean up long-title responsive feature`);
});

test("a feature with a wide multi-digit rollup does not overflow at narrow widths (task 103)", async ({
  page,
}) => {
  test.skip(!storeIsLocal(), "store is not local to the test runner");
  // The head overflow scaled with the rollup's digit count (2px at 0/1, 17px
  // at 0/300); the long-title test above only exercises a 1-digit rollup, so
  // pin the wide 3-digit case that spilled .feature-head before it wrapped.
  const res = await page.request.post(`${BASE_URL}/api/projects/${PROJECT}/features`, {
    data: { description: `e2e wide-rollup ${Date.now()}` },
  });
  expect(res.status()).toBe(201);
  const slug = (await res.json()).slug as string;
  // 150 task refs -> a 3-digit "0/150 tasks done" rollup (refs may be missing;
  // the denominator counts them all, which is what widened the head row).
  linkTasksToFeature(slug, Array.from({ length: 150 }, (_, i) => i + 1));

  await gotoBoard(page);
  await Promise.all([
    page.waitForResponse((r) => r.url().includes(`/api/projects/${PROJECT}/features`)),
    page.locator("#tab-features").click(),
  ]);
  const cardSel = `.feature-card[data-slug="${slug}"]`;
  await expect(page.locator(`${cardSel} .rollup`)).toContainText("0/150");

  for (const width of NARROW) {
    await page.setViewportSize({ width, height: 900 });
    expect(
      await hasHorizontalOverflow(page),
      `@${width}: horizontal overflow with a 3-digit rollup`
    ).toBe(false);
  }

  await page.request.delete(`${BASE_URL}/api/projects/${PROJECT}/features/${slug}`);
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
  commitStore(`${PROJECT}/features`, `chore(${PROJECT}): clean up wide-table responsive feature`);
});

test("a wide code block in a task dialog scrolls in its own box, not the page", async ({ page }) => {
  test.skip(!storeIsLocal(), "store is not local to the test runner");

  // A code block relies on `.md pre { overflow-x: auto }` (a distinct rule from
  // the wide-table box above): white-space is `pre`, so it cannot wrap and must
  // scroll inside its own box. Without that rule a long code line would push the
  // page sideways. Code blocks are among the most common task-body content.
  const t = await createTaskViaAPI(page.request, uniqueDesc("wide-code"));
  const longLine = "const x = " + "a_very_long_identifier_".repeat(20) + ";";
  await page.request.put(`${BASE_URL}/api/projects/${PROJECT}/tasks/${t.num}`, {
    data: { body: "```\n" + longLine + "\n```\n" },
  });

  await page.goto(`/#/p/${PROJECT}/task/${t.num}`);
  await expect(page.locator("#dialog-body.md pre")).toBeVisible();

  for (const width of [1280, 480]) {
    await page.setViewportSize({ width, height: 900 });
    expect(await hasHorizontalOverflow(page), `@${width}: page overflows horizontally`).toBe(false);
    const box = await page.evaluate(() => {
      const pre = document.querySelector("#dialog-body.md pre") as HTMLElement;
      const r = pre.getBoundingClientRect();
      return {
        withinViewport: r.right <= window.innerWidth + 1 && r.left >= -1,
        scrolls: pre.scrollWidth > pre.clientWidth,
        overflowX: getComputedStyle(pre).overflowX,
      };
    });
    expect(box.withinViewport, `@${width}: code box overflows viewport`).toBe(true);
    expect(box.overflowX, `@${width}: code block is not a scroll box`).toBe("auto");
    expect(box.scrolls, `@${width}: the long line should exceed the box`).toBe(true);
  }

  await finishTask(page.request, t.num);
});

test("a spec's long unbreakable inline content wraps and never scrolls the page", async ({
  page,
}) => {
  test.skip(!storeIsLocal(), "store is not local to the test runner");

  // People paste long URLs and long code identifiers into specs. Unlike a wide
  // table (its own scroll box), inline body text must break/wrap so it never
  // widens the card or scrolls the page at mobile width -- a plausible CSS
  // regression the title/table tests would not catch.
  const res = await page.request.post(`${BASE_URL}/api/projects/${PROJECT}/features`, {
    data: { description: `e2e long-inline ${Date.now()}` },
  });
  expect(res.status()).toBe(201);
  const slug = (await res.json()).slug as string;

  const url = "https://example.com/" + "a/very/long/unbreakable/path/segment".repeat(4) + "/1234567890";
  const code = "aVeryLongInlineCodeIdentifierWithNoSpacesWhatsoeverToForceOverflowIfUnhandled";
  appendFeatureBody(slug, `\n## refs\n\nSee ${url} and \`${code}\`.\n`);

  await gotoBoard(page);
  await Promise.all([
    page.waitForResponse((r) => r.url().includes(`/api/projects/${PROJECT}/features`)),
    page.locator("#tab-features").click(),
  ]);
  const cardSel = `.feature-card[data-slug="${slug}"]`;
  await page.locator(`${cardSel} summary`).click();
  await expect(page.locator(`${cardSel} .md`)).toContainText("refs");

  for (const width of NARROW) {
    await page.setViewportSize({ width, height: 900 });
    expect(await hasHorizontalOverflow(page), `@${width}: horizontal overflow`).toBe(false);
    // The spec body box itself stays within the viewport (content wrapped,
    // not spilling past the right edge).
    const within = await page.evaluate((sel) => {
      const md = document.querySelector(`${sel} .md`) as HTMLElement;
      const r = md.getBoundingClientRect();
      return r.right <= window.innerWidth + 1 && r.left >= -1 && md.scrollWidth <= md.clientWidth + 1;
    }, cardSel);
    expect(within, `@${width}: spec body overflows its box`).toBe(true);
  }

  fs.rmSync(path.join(FEATURES_DIR, `${slug}.md`));
  commitStore(`${PROJECT}/features`, `chore(${PROJECT}): clean up long-inline responsive feature`);
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
