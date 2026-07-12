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
