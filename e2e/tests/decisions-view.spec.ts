import { test, expect } from "@playwright/test";
import {
  BASE_URL,
  PROJECT,
  createTaskViaAPI,
  decisionPoseSupported,
  finishTask,
  gotoBoard,
  poseDecision,
  storeIsLocal,
  uniqueDesc,
} from "../helpers";

/**
 * Top-level decisions view (task 105): a cross-project inbox (`/api/decisions`,
 * #/decisions) plus a per-project scope (`/api/projects/{p}/decisions`,
 * #/p/<project>/decisions). The header pill shows the cross-project count and
 * opens the view; each row deep-links into the task dialog where the 091 widget
 * answers it. Answering removes the row (task 106 wired the view into the
 * refresh paths). Posing needs a current CLI + local store.
 */

const base = `${BASE_URL}/api/projects/${PROJECT}`;

test.skip(() => !storeIsLocal(), "store is not local to the test runner");
test.skip(() => !decisionPoseSupported(), "taskman binary cannot pose decisions (stale CLI)");

/** Answer any live sandbox decision so the project starts each test clean. */
async function clearSandboxDecisions(request: import("@playwright/test").APIRequestContext) {
  const rows = await (await request.get(`${base}/decisions`)).json();
  for (const r of rows) {
    await request.post(`${base}/tasks/${r.num}/answer`, { data: { choice: "__any__" } }).catch(() => {});
    // choice may be invalid; fall back to finishing so it drops out of the list.
    await request.post(`${base}/tasks/${r.num}/status`, { data: { status: "done" } });
  }
}

test("the cross-project and per-project endpoints list a live decision", async ({ request }) => {
  const t = await createTaskViaAPI(request, uniqueDesc("dv-api"));
  poseDecision(t.num, "Endpoint shape ok?", ["Yes::a", "No::b"]);

  const all = await (await request.get(`${BASE_URL}/api/decisions`)).json();
  const mine = all.find((r: { project: string; num: number }) => r.project === PROJECT && r.num === t.num);
  expect(mine, "cross-project inbox includes the sandbox decision").toBeTruthy();
  expect(mine.question).toBe("Endpoint shape ok?");
  expect(mine.options).toBe(2);
  expect(mine.title).toContain("dv-api");

  const proj = await (await request.get(`${base}/decisions`)).json();
  expect(proj.some((r: { num: number }) => r.num === t.num)).toBe(true);
  // The per-project list must not carry other projects' rows.
  expect(proj.every((r: { project?: string }) => !r.project || r.project === PROJECT)).toBe(true);

  await request.post(`${base}/tasks/${t.num}/answer`, { data: { choice: "Yes" } });
  await finishTask(request, t.num);
});

test("the pill opens the inbox listing the decision; the scope toggle narrows to this project", async ({
  page,
  request,
}) => {
  const t = await createTaskViaAPI(request, uniqueDesc("dv-pill"));
  poseDecision(t.num, "Pill opens inbox?", ["Yes::a", "No::b"]);
  await gotoBoard(page);

  const pill = page.locator("#decisions-pill");
  await expect(pill).toBeVisible();
  await expect(pill).toContainText("decision");
  await pill.click();

  await expect.poll(() => page.evaluate(() => location.hash)).toBe("#/decisions");
  const row = page.locator(".decision-row", { hasText: "Pill opens inbox?" });
  await expect(row).toBeVisible();
  await expect(row).toContainText(`${PROJECT} ${String(t.num).padStart(3, "0")}`);
  await expect(page.locator(".decisions-bar button.active")).toHaveText("all projects");

  // Narrow to this project.
  await page.locator(".decisions-bar button", { hasText: "this project" }).click();
  await expect.poll(() => page.evaluate(() => location.hash)).toBe(`#/p/${PROJECT}/decisions`);
  await expect(page.locator(".decision-row", { hasText: "Pill opens inbox?" })).toBeVisible();

  await request.post(`${base}/tasks/${t.num}/answer`, { data: { choice: "Yes" } });
  await finishTask(request, t.num);
});

test("clicking a decision row opens the task dialog widget, and answering removes the row (105, 106)", async ({
  page,
  request,
}) => {
  const t = await createTaskViaAPI(request, uniqueDesc("dv-answer"));
  poseDecision(t.num, "Answer removes the row?", ["Remove it::a", "Keep it::b"]);
  await gotoBoard(page);
  await page.locator("#decisions-pill").click();
  await page.locator(".decisions-bar button", { hasText: "this project" }).click();

  const row = page.locator(".decision-row", { hasText: "Answer removes the row?" });
  await expect(row).toBeVisible();
  await row.click();

  // Deep-links to the task and shows the 091 answer widget.
  await expect.poll(() => page.evaluate(() => location.hash)).toBe(`#/p/${PROJECT}/task/${t.num}`);
  await expect(page.locator(".decision-box .decision-question")).toContainText("Answer removes the row?");

  // Answer, then the row is gone from the list and the pill reflects it (106).
  await page.locator(".decision-option").first().click();
  await expect(page.locator(".decision-row", { hasText: "Answer removes the row?" })).toHaveCount(0);

  await finishTask(request, t.num);
});

test("the this-project view shows an empty state when the project has no decisions", async ({
  page,
  request,
}) => {
  await clearSandboxDecisions(request);
  await gotoBoard(page);
  await page.goto(`/#/p/${PROJECT}/decisions`);
  await expect(page.locator("#decisions .empty")).toHaveText("no decisions awaiting you");
  await expect(page.locator(".decision-row")).toHaveCount(0);
});

test("the open inbox drops a decision answered out-of-band on window focus (task 106)", async ({
  page,
  request,
}) => {
  const t = await createTaskViaAPI(request, uniqueDesc("dv-focus"));
  poseDecision(t.num, "Out-of-band answer refreshes?", ["Yes::a", "No::b"]);
  await gotoBoard(page);
  await page.locator("#decisions-pill").click();
  const row = page.locator(".decision-row", { hasText: "Out-of-band answer refreshes?" });
  await expect(row).toBeVisible();

  // Another session answers it directly (not through this tab).
  expect((await request.post(`${base}/tasks/${t.num}/answer`, { data: { choice: "Yes" } })).ok()).toBeTruthy();
  // Regaining focus refreshes the decisions view (085/106), dropping the row.
  await page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await expect(row).toHaveCount(0);

  await finishTask(request, t.num);
});
