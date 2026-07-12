import { test, expect } from "@playwright/test";
import {
  BASE_URL,
  PROJECT,
  card,
  createTaskViaAPI,
  finishTask,
  gotoBoard,
  openCard,
  storeIsLocal,
  uniqueDesc,
} from "../helpers";

/**
 * Lane assignment from the web UI (task 109): the task dialog's lane select
 * moves a task between lanes (or clears it) through POST tasks/{n}/lane, and
 * the card's lane badge, the lane filter, and swimlane grouping follow. Also
 * covers a brand-new lane via the "new lane..." prompt. These specs mutate the
 * sandbox, so they need the store local.
 */

const base = `${BASE_URL}/api/projects/${PROJECT}`;

test.skip(() => !storeIsLocal(), "store is not local to the test runner");

test("the dialog lane select assigns and clears a task's lane, and the badge follows", async ({
  page,
  request,
}) => {
  const t = await createTaskViaAPI(request, uniqueDesc("lane-ui")); // starts laneless
  await gotoBoard(page);
  await expect(card(page, t.num).locator(".badge.lane")).toHaveCount(0);

  // Assign an existing lane (seeds provide "web") via the dialog select.
  await openCard(page, t.num);
  await Promise.all([
    page.waitForResponse((r) => r.url().endsWith(`/tasks/${t.num}/lane`) && r.request().method() === "POST"),
    page.locator("#lane-select").selectOption("web"),
  ]);
  await expect(card(page, t.num).locator(".badge.lane")).toHaveText("web");
  // Persisted server-side.
  const afterSet = (await (await request.get(`${base}/tasks`)).json()).tasks.find(
    (x: { num: number }) => x.num === t.num
  );
  expect(afterSet.lane).toBe("web");

  // Clear it back to no lane.
  await openCard(page, t.num);
  await Promise.all([
    page.waitForResponse((r) => r.url().endsWith(`/tasks/${t.num}/lane`) && r.request().method() === "POST"),
    page.locator("#lane-select").selectOption(""),
  ]);
  await expect(card(page, t.num).locator(".badge.lane")).toHaveCount(0);

  await finishTask(request, t.num);
});

test("the new-lane prompt assigns a brand-new lane", async ({ page, request }) => {
  const t = await createTaskViaAPI(request, uniqueDesc("lane-new"));
  const laneName = `e2elane${Date.now()}`;
  await gotoBoard(page);
  await openCard(page, t.num);

  page.once("dialog", (d) => d.accept(laneName));
  await Promise.all([
    page.waitForResponse((r) => r.url().endsWith(`/tasks/${t.num}/lane`) && r.request().method() === "POST"),
    page.locator("#lane-select").selectOption("__new__"),
  ]);
  await expect(card(page, t.num).locator(".badge.lane")).toHaveText(laneName);

  await finishTask(request, t.num);
});

test("the lane API sets, clears, and 404s an unknown task", async ({ request }) => {
  const t = await createTaskViaAPI(request, uniqueDesc("lane-api"));

  expect((await request.post(`${base}/tasks/${t.num}/lane`, { data: { lane: "web" } })).status()).toBe(200);
  const set = (await (await request.get(`${base}/tasks`)).json()).tasks.find((x: { num: number }) => x.num === t.num);
  expect(set.lane).toBe("web");

  expect((await request.post(`${base}/tasks/${t.num}/lane`, { data: { lane: "" } })).status()).toBe(200);
  const cleared = (await (await request.get(`${base}/tasks`)).json()).tasks.find((x: { num: number }) => x.num === t.num);
  expect(cleared.lane).toBe("");

  expect((await request.post(`${base}/tasks/999999/lane`, { data: { lane: "web" } })).status()).toBe(404);

  await finishTask(request, t.num);
});

test("an over-long lane is refused with a clean message, not a raw filesystem error (task 117)", async ({
  request,
}) => {
  const t = await createTaskViaAPI(request, uniqueDesc("lane-toolong"));
  expect((await request.post(`${base}/tasks/${t.num}/lane`, { data: { lane: "web" } })).status()).toBe(200);

  const res = await request.post(`${base}/tasks/${t.num}/lane`, { data: { lane: "x".repeat(250) } });
  expect(res.status()).toBe(400);
  const err = (await res.json()).error as string;
  expect(err).toContain("lane too long");
  expect(err, "no raw ENAMETOOLONG surfaced").not.toMatch(/file name too long/i);
  expect(err, "no internal filename leaked").not.toMatch(/\.md/);

  // The prior lane survives -- the refusal happens before any rename.
  const still = (await (await request.get(`${base}/tasks`)).json()).tasks.find(
    (x: { num: number }) => x.num === t.num
  );
  expect(still.lane).toBe("web");

  await finishTask(request, t.num);
});

test("an in-limit lane whose combined basename would exceed the filename limit is refused cleanly (task 117)", async ({
  request,
}) => {
  // A valid (<200) but long slug plus an in-limit (<=40) lane can still push the
  // whole basename past 255; that must be caught before any rename with an
  // actionable message, not a raw ENAMETOOLONG.
  const longDesc = Array.from({ length: 60 }, () => "ab").join("-") + `-${Date.now()}`; // ~193-char slug, unique
  const t = await createTaskViaAPI(request, longDesc);

  const res = await request.post(`${base}/tasks/${t.num}/lane`, { data: { lane: "a".repeat(40) } });
  expect(res.status()).toBe(400);
  const err = (await res.json()).error as string;
  expect(err).toContain("name too long");
  expect(err, "no raw ENAMETOOLONG surfaced").not.toMatch(/file name too long/i);

  // Still laneless -- no partial rename.
  const still = (await (await request.get(`${base}/tasks`)).json()).tasks.find(
    (x: { num: number }) => x.num === t.num
  );
  expect(still.lane).toBe("");

  await finishTask(request, t.num);
});
