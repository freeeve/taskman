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
