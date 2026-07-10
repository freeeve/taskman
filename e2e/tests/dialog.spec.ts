import { test, expect, type Page } from "@playwright/test";
import {
  TINY_PNG,
  card,
  createTaskViaAPI,
  finishTask,
  gotoBoard,
  openCard,
  uniqueDesc,
} from "../helpers";
import { BASE_URL, PROJECT } from "../helpers";

/**
 * Task detail dialog layout: the near-fullscreen frame (min(1400px, 96vw)
 * by 94vh), the body scrolling inside while the file header and action bar
 * stay pinned, and width fitting a narrow viewport. Locks in the layout
 * from task 018.
 */

const base = `${BASE_URL}/api/projects/${PROJECT}`;

/** Attach n screenshots to a task so its dialog body overflows. */
async function padBody(page: Page, num: number, n: number): Promise<void> {
  for (let i = 0; i < n; i++) {
    const res = await page.request.post(`${base}/tasks/${num}/screenshots`, {
      multipart: { file: { name: "s.png", mimeType: "image/png", buffer: TINY_PNG } },
    });
    expect(res.status()).toBe(201);
  }
}

/** Measured geometry of the open dialog and its parts. */
async function dialogMetrics(page: Page) {
  return page.evaluate(() => {
    const d = document.querySelector("#task-dialog") as HTMLElement;
    const body = document.querySelector("#dialog-body") as HTMLElement;
    const actions = document.querySelector("#dialog-actions") as HTMLElement;
    const r = d.getBoundingClientRect();
    const ar = actions.getBoundingClientRect();
    return {
      display: getComputedStyle(d).display,
      width: r.width,
      height: r.height,
      bodyScrollable: body.scrollHeight > body.clientHeight + 1,
      actionsBottom: ar.bottom,
      innerWidth: window.innerWidth,
      innerHeight: window.innerHeight,
    };
  });
}

test("the detail dialog spans most of the viewport as a flex column", async ({ page }) => {
  const t = await createTaskViaAPI(page.request, uniqueDesc("dialog-size"));
  await gotoBoard(page);
  await openCard(page, t.num);

  const m = await dialogMetrics(page);
  expect(m.display).toBe("flex");
  expect(m.width / m.innerWidth).toBeGreaterThan(0.9);
  expect(m.height / m.innerHeight).toBeGreaterThan(0.9);
  expect(m.width).toBeLessThanOrEqual(m.innerWidth);

  await finishTask(page.request, t.num);
});

test("a long body scrolls inside the dialog while the action bar stays pinned", async ({
  page,
}) => {
  const t = await createTaskViaAPI(page.request, uniqueDesc("dialog-scroll"));
  await padBody(page, t.num, 8);
  await gotoBoard(page);
  await openCard(page, t.num);

  const before = await dialogMetrics(page);
  expect(before.bodyScrollable).toBe(true);
  expect(before.actionsBottom).toBeLessThanOrEqual(before.innerHeight + 1);

  await page.locator("#dialog-body").evaluate((el) => (el.scrollTop = el.scrollHeight));
  const after = await dialogMetrics(page);
  expect(Math.abs(after.actionsBottom - before.actionsBottom)).toBeLessThan(2);
  await expect(page.locator("#dialog-actions button").first()).toBeVisible();

  await finishTask(page.request, t.num);
});

test("the dialog fits a narrow viewport without overflowing its width", async ({ page }) => {
  const t = await createTaskViaAPI(page.request, uniqueDesc("dialog-mobile"));
  await page.setViewportSize({ width: 390, height: 844 });
  await gotoBoard(page);
  await openCard(page, t.num);

  const m = await dialogMetrics(page);
  expect(m.width).toBeLessThanOrEqual(m.innerWidth);
  expect(m.width / m.innerWidth).toBeGreaterThan(0.9);

  await finishTask(page.request, t.num);
});

test("closing the dialog collapses it so neither view is obscured", async ({ page }) => {
  const t = await createTaskViaAPI(page.request, uniqueDesc("dialog-close"));
  await gotoBoard(page);
  await openCard(page, t.num);
  await page.locator("#dialog-close").click();
  await expect(page.locator("#task-dialog")).toBeHidden();
  await expect(card(page, t.num)).toBeVisible();
  await finishTask(page.request, t.num);
});
