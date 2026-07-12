import { test, expect, type Page } from "@playwright/test";
import {
  TINY_PNG,
  appendTaskBody,
  card,
  createTaskViaAPI,
  finishTask,
  gotoBoard,
  openCard,
  storeIsLocal,
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

test("wide content in the dialog body scrolls and external links open in a new tab", async ({
  page,
}) => {
  test.skip(!storeIsLocal(), "store is not local to the test runner");
  const t = await createTaskViaAPI(page.request, uniqueDesc("dialog-md"));
  // The 054 (link target) and 059 (wide-table scroll box) fixes live in the
  // shared renderBody/.md; assert they hold at the dialog render site too, not
  // just feature specs. Task bodies are not API-editable, so write the file.
  const cols = 15;
  const cell = (p: string) => "| " + Array.from({ length: cols }, (_, i) => `${p}${i}`).join(" | ") + " |";
  const sep = "| " + Array.from({ length: cols }, () => "---").join(" | ") + " |";
  appendTaskBody(
    t.file,
    `\n## detail\n\n${cell("c")}\n${sep}\n${cell("v")}\n\nSee [docs](https://example.com/ref).\n`
  );

  await page.setViewportSize({ width: 320, height: 900 });
  await gotoBoard(page);
  await openCard(page, t.num);
  await expect(page.locator("#dialog-body table")).toBeVisible();

  const report = await page.evaluate(() => {
    const vw = window.innerWidth;
    const dlg = document.querySelector("#task-dialog")!.getBoundingClientRect();
    const table = document.querySelector("#dialog-body table") as HTMLElement;
    const tr = table.getBoundingClientRect();
    const link = document.querySelector('#dialog-body a[href^="https://"]');
    return {
      docOverflow: document.documentElement.scrollWidth > vw + 1,
      dialogWithin: dlg.right <= vw + 1 && dlg.left >= -1,
      tableWithin: tr.right <= vw + 1 && tr.left >= -1,
      tableOverflowX: getComputedStyle(table).overflowX,
      linkTarget: link?.getAttribute("target") ?? null,
      linkRel: link?.getAttribute("rel") ?? null,
    };
  });
  expect(report.docOverflow, "page overflowed").toBe(false);
  expect(report.dialogWithin, "dialog overflowed viewport").toBe(true);
  expect(report.tableWithin, "table overflowed viewport").toBe(true);
  expect(report.tableOverflowX, "table not a scroll box").toBe("auto");
  expect(report.linkTarget).toBe("_blank");
  expect(report.linkRel).toBe("noopener noreferrer");

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

test("a backdrop click closes the dialog; a click inside or a drag out does not (task 090)", async ({
  page,
}) => {
  test.skip(!storeIsLocal(), "store is not local to the test runner");
  const t = await createTaskViaAPI(page.request, uniqueDesc("dialog-dismiss"));
  await gotoBoard(page);
  await openCard(page, t.num);
  const dialog = page.locator("#task-dialog");
  await expect(dialog).toBeVisible();

  // A click inside the content keeps it open.
  await page.locator("#dialog-body").click();
  await expect(dialog).toBeVisible();

  // A press that starts inside and releases on the backdrop must NOT close
  // (guards a text selection dragged out of the edit field).
  const body = await page.locator("#dialog-body").boundingBox();
  await page.mouse.move(body!.x + 12, body!.y + 12);
  await page.mouse.down();
  await page.mouse.move(5, 5);
  await page.mouse.up();
  await expect(dialog).toBeVisible();

  // A clean click on the backdrop (top-left corner, outside the content) closes.
  await page.mouse.click(5, 5);
  await expect(dialog).toBeHidden();

  await finishTask(page.request, t.num);
});

test("a read-only task dialog still backdrop-closes without a discard prompt (090 preserved after task 101)", async ({
  page,
}) => {
  test.skip(!storeIsLocal(), "store is not local to the test runner");
  // The unsaved-edit guard (101) must only fire while editing; a plain view
  // has no editor fields, so light dismiss stays frictionless.
  const t = await createTaskViaAPI(page.request, uniqueDesc("view-dismiss"));
  await gotoBoard(page);
  await openCard(page, t.num);
  await expect(page.locator("#task-dialog")).toBeVisible();

  let prompted = false;
  page.on("dialog", (d) => {
    prompted = true;
    d.dismiss();
  });
  await page.mouse.click(5, 5);
  await expect(page.locator("#task-dialog")).toBeHidden();
  expect(prompted, "a read-only dialog must not prompt on dismiss").toBe(false);

  await finishTask(page.request, t.num);
});
