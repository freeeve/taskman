import { test, expect } from "@playwright/test";
import {
  BASE_URL,
  PROJECT,
  createTaskViaAPI,
  finishTask,
  storeIsLocal,
  uniqueDesc,
} from "../helpers";

/**
 * Hash deep links (task 082): #/p/<project>[/features|/activity|/task/<n>|
 * /feature/<slug>] restore a project + view + open item on load. Includes the
 * regression for task 086, where a feature deep link with a second panel open
 * fed rebuild-fired toggle events back into the hash and looped between the two
 * features. These specs drive the sandbox, so need the store local.
 */

const base = `${BASE_URL}/api/projects/${PROJECT}`;

test.skip(() => !storeIsLocal(), "store is not local to the test runner");

async function makeFeature(request: import("@playwright/test").APIRequestContext, tag: string) {
  const res = await request.post(`${base}/features`, { data: { description: uniqueDesc(tag) } });
  expect(res.status()).toBe(201);
  return (await res.json()).slug as string;
}

test("a feature deep link opens that spec panel on the features view", async ({ page, request }) => {
  const slug = await makeFeature(request, "dl-open");
  await page.goto(`/#/p/${PROJECT}/feature/${slug}`);

  await expect(page.locator("#tab-features")).toHaveClass(/active/);
  await expect(page.locator(`.feature-card[data-slug="${slug}"] details[open]`)).toHaveCount(1);
});

test("a task deep link opens that task's dialog over the board", async ({ page, request }) => {
  const t = await createTaskViaAPI(request, uniqueDesc("dl-task"));
  await page.goto(`/#/p/${PROJECT}/task/${t.num}`);

  await expect(page.locator("#task-dialog")).toBeVisible();
  await expect(page.locator("#task-dialog")).toContainText(t.title);

  await finishTask(request, t.num);
});

test("a bogus feature slug in the hash falls back to the features view without error", async ({
  page,
}) => {
  await page.goto(`/#/p/${PROJECT}/feature/no-such-feature-zzz-999`);
  // The view still renders; no matching card, no crash, no error state.
  await expect(page.locator("#features .features-bar")).toBeVisible();
  await expect(page.locator(`.feature-card[data-slug="no-such-feature-zzz-999"]`)).toHaveCount(0);
});

test("a shipped feature's deep link still opens its panel (slug, not slug.done)", async ({
  page,
  request,
}) => {
  // Shipped features live in slug.done.md but the API/card slug stays the base
  // slug, so the deep link must resolve to the shipped card just the same.
  const slug = await makeFeature(request, "dl-shipped");
  expect((await request.post(`${base}/features/${slug}/done`)).ok()).toBeTruthy();

  await page.goto(`/#/p/${PROJECT}/feature/${slug}`);
  await expect(page.locator("#tab-features")).toHaveClass(/active/);
  const card = page.locator(`.feature-card[data-slug="${slug}"]`);
  await expect(card).toBeVisible();
  await expect(card.locator(".badge", { hasText: "shipped" })).toBeVisible();
  await expect(card.locator("details[open]")).toHaveCount(1);
});

test("a feature deep link with a second panel open does not loop the hash (task 086)", async ({
  page,
  request,
}) => {
  const slugA = await makeFeature(request, "dl-loop-a");
  const slugB = await makeFeature(request, "dl-loop-b");

  // Count every hashchange from first script execution.
  await page.addInitScript(() => {
    (window as unknown as { __hc: number }).__hc = 0;
    window.addEventListener("hashchange", () => (window as unknown as { __hc: number }).__hc++);
  });

  // Deep-link to A -> its panel opens.
  await page.goto(`/#/p/${PROJECT}/feature/${slugA}`);
  await expect(page.locator(`.feature-card[data-slug="${slugA}"] details[open]`)).toHaveCount(1);

  // Open B's panel too, then trigger a re-render the way the focus refresh does.
  await page.locator(`.feature-card[data-slug="${slugB}"] summary`).click();
  await page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await page.waitForTimeout(1000);

  // Before the fix this ran to dozens of hashchanges, ping-ponging A<->B.
  const hc = await page.evaluate(() => (window as unknown as { __hc: number }).__hc);
  expect(hc, `hash oscillated (${hc} changes) -- the 086 loop is back`).toBeLessThan(10);
  // The hash settled on one of the two features, not the list or elsewhere.
  const hash = await page.evaluate(() => location.hash);
  expect(hash).toMatch(new RegExp(`/feature/(${slugA}|${slugB})$`));
});

test("opening a task from a feature chip deep-links to it, and Back restores the features view (task 092)", async ({
  page,
  request,
}) => {
  // Opening a task over the features tab must set the task hash (shareable,
  // and Back closes the dialog) -- not leave it on /features. Regression for
  // the currentHash() ordering where the tab view masked the open dialog.
  const t = await createTaskViaAPI(request, uniqueDesc("dl-chip"));
  const slug = await makeFeature(request, "dl-chip-feat");
  expect((await request.put(`${base}/features/${slug}/tasks`, { data: { tasks: [t.num] } })).ok()).toBeTruthy();

  await page.goto(`/#/p/${PROJECT}/features`);
  const chip = page.locator(`.feature-card[data-slug="${slug}"] button.chip`).first();
  await expect(chip).toBeVisible();
  await chip.click();

  // The dialog opens and the hash reflects the task, so the URL is shareable.
  await expect(page.locator("#task-dialog")).toBeVisible();
  await expect
    .poll(() => page.evaluate(() => location.hash))
    .toBe(`#/p/${PROJECT}/task/${t.num}`);

  // Back closes the dialog and returns to the features view it was opened over.
  await page.goBack();
  await expect(page.locator("#task-dialog")).toBeHidden();
  await expect.poll(() => page.evaluate(() => location.hash)).toBe(`#/p/${PROJECT}/features`);

  await request.delete(`${base}/features/${slug}`);
  await finishTask(request, t.num);
});
