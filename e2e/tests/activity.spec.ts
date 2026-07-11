import { test, expect } from "@playwright/test";
import { BASE_URL, PROJECT, gotoBoard, storeIsLocal, uniqueDesc } from "../helpers";

/**
 * Activity view (task 079): a read-only audit trail of the project's commits
 * (one per mutation), newest first, with the `chore(<project>):` prefix
 * stripped into a readable summary and a relative timestamp. Scoped to the
 * project by construction (git log -- project/), so other projects' commits
 * never appear. These specs mutate the sandbox, so need the store local.
 */

const base = `${BASE_URL}/api/projects/${PROJECT}`;

test.skip(() => !storeIsLocal(), "store is not local to the test runner");

test("the activity tab shows a just-made mutation at the top, prefix stripped", async ({
  page,
  request,
}) => {
  const created = await request.post(`${base}/features`, {
    data: { description: uniqueDesc("activity-feat") },
  });
  expect(created.status()).toBe(201);
  const { slug } = await created.json();

  await gotoBoard(page);
  await Promise.all([
    page.waitForResponse((r) => r.url().includes("/activity")),
    page.locator("#tab-activity").click(),
  ]);
  await expect(page.locator("#activity")).toBeVisible();

  // The newest row summarizes the feature creation, with the chore(project):
  // prefix stripped, and carries a relative time.
  const first = page.locator("#activity .activity-row").first();
  await expect(first.locator(".activity-summary")).toContainText(`feature ${slug}`);
  await expect(first.locator(".activity-summary")).not.toContainText("chore(");
  await expect(first.locator(".activity-time")).not.toBeEmpty();
  // The full subject is preserved on hover (title attribute).
  await expect(first.locator(".activity-summary")).toHaveAttribute(
    "title",
    `chore(${PROJECT}): feature ${slug}`
  );
});

test("the activity API lists mutations newest-first with the project prefix stripped", async ({
  request,
}) => {
  const a = await request.post(`${base}/features`, { data: { description: uniqueDesc("activity-a") } });
  const slugA = (await a.json()).slug as string;
  const b = await request.post(`${base}/features`, { data: { description: uniqueDesc("activity-b") } });
  const slugB = (await b.json()).slug as string;

  const entries = (await (await request.get(`${base}/activity?limit=50`)).json()) as {
    commit: string;
    subject: string;
    summary: string;
    time: string;
  }[];
  const idx = (needle: string) => entries.findIndex((e) => e.summary.includes(needle));
  const iA = idx(slugA);
  const iB = idx(slugB);
  expect(iA, "feature A should appear in the log").toBeGreaterThanOrEqual(0);
  expect(iB, "feature B should appear in the log").toBeGreaterThanOrEqual(0);
  // B was created after A, so it is nearer the top (newest first).
  expect(iB).toBeLessThan(iA);

  const eB = entries[iB];
  expect(eB.summary).toContain(`feature ${slugB}`);
  expect(eB.summary).not.toContain("chore(");
  expect(eB.subject).toContain(`chore(${PROJECT}): feature ${slugB}`);
  expect(eB.commit).toMatch(/^[0-9a-f]{7,40}$/);
  expect(eB.time).toBeTruthy();
});

test("the activity view refreshes on focus, surfacing an out-of-band mutation (task 085)", async ({
  page,
  request,
}) => {
  await gotoBoard(page);
  await Promise.all([
    page.waitForResponse((r) => r.url().includes("/activity")),
    page.locator("#tab-activity").click(),
  ]);

  // An out-of-band mutation (separate client) the open activity tab doesn't know
  // about yet -- the store is multi-writer.
  const created = await request.post(`${base}/features`, {
    data: { description: uniqueDesc("activity-focus") },
  });
  const { slug } = await created.json();
  const entry = page.locator("#activity .activity-summary", { hasText: `feature ${slug}` });
  await expect(entry).toHaveCount(0);

  // Regaining focus refreshes the activity list (task 085 taught refreshStale
  // about the activity tab); the new entry appears without a tab switch.
  await Promise.all([
    page.waitForResponse((r) => r.url().includes("/activity")),
    page.evaluate(() => window.dispatchEvent(new Event("focus"))),
  ]);
  await expect(entry.first()).toBeVisible();
});
