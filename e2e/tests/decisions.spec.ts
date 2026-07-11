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
 * Interactive decisions (task 091): an agent poses a structured question on a
 * deferred task via the CLI (`defer -question -option ...`); the web surfaces
 * it (a header pill, a "decision needed" card badge, and an answer dialog with
 * one button per labelled option and its explanation). Answering records the
 * choice, lifts the deferral, and promotes the task to the top of the order.
 * A plain resume must refuse a live decision, and invalid/duplicate answers
 * are rejected. Posing needs a current CLI, so these gate on both the local
 * store and a decision-capable binary.
 */

const base = `${BASE_URL}/api/projects/${PROJECT}`;
const Q = "Retry transient failures inline or queue them?";
const OPTS = ["Retry inline::Simpler; a slow endpoint blocks the run", "Queue for later::Keeps moving; needs a durable queue"];

test.skip(() => !storeIsLocal(), "store is not local to the test runner");
test.skip(() => !decisionPoseSupported(), "taskman binary cannot pose decisions (stale CLI)");

test("a posed decision defers the task and exposes its structured shape on the wire", async ({
  request,
}) => {
  const t = await createTaskViaAPI(request, uniqueDesc("dec-wire"));
  poseDecision(t.num, Q, OPTS);

  const listed = (await (await request.get(`${base}/tasks`)).json()).tasks.find(
    (x: { num: number }) => x.num === t.num
  );
  expect(listed.deferred).toBe(true);
  expect(listed.has_decision).toBe(true);

  const detail = await (await request.get(`${base}/tasks/${t.num}`)).json();
  expect(detail.decision.question).toBe(Q);
  expect(detail.decision.allow_other).toBe(true);
  expect(detail.decision.options).toEqual([
    { label: "Retry inline", explain: "Simpler; a slow endpoint blocks the run" },
    { label: "Queue for later", explain: "Keeps moving; needs a durable queue" },
  ]);

  await finishTask(request, t.num);
});

test("the header pill and card badge surface an unanswered decision", async ({ page, request }) => {
  const t = await createTaskViaAPI(request, uniqueDesc("dec-pill"));
  poseDecision(t.num, Q, OPTS);
  await gotoBoard(page);

  const pill = page.locator("#decisions-pill");
  await expect(pill).toBeVisible();
  await expect(pill).toContainText("decision");

  // A deferred task is hidden until the pill reveals it; clicking the pill
  // turns on the deferred filter so the badged card shows.
  await pill.click();
  const card = page.locator(`[data-num="${t.num}"]`);
  await expect(card).toBeVisible();
  await expect(card.locator(".badge.decision")).toHaveText("decision needed");

  await finishTask(request, t.num);
});

test("answering an option in the dialog un-defers the task and promotes it to the top", async ({
  page,
  request,
}) => {
  const t = await createTaskViaAPI(request, uniqueDesc("dec-answer"));
  poseDecision(t.num, Q, OPTS);
  await gotoBoard(page);
  await page.locator("#decisions-pill").click();
  await page.locator(`[data-num="${t.num}"]`).click();

  // The dialog renders the question and one button per option with its explain.
  const box = page.locator(".decision-box");
  await expect(box.locator(".decision-question")).toContainText(Q);
  const opts = box.locator(".decision-option");
  await expect(opts).toHaveCount(2);
  await expect(box).toContainText("Keeps moving; needs a durable queue");
  await expect(box.locator(".decision-other")).toBeVisible();

  // Answer the second option.
  await opts.nth(1).click();
  await expect(page.locator("#task-dialog")).toBeHidden();

  // Server state: un-deferred, no live decision, promoted to the top of order.
  const after = await (await request.get(`${base}/tasks`)).json();
  const t2 = after.tasks.find((x: { num: number }) => x.num === t.num);
  expect(t2.deferred).toBe(false);
  expect(t2.status).toBe("pending");
  expect(t2.has_decision).toBe(false);
  expect(after.order[0]).toBe(t.num);

  // The choice is recorded as an answered block in the body.
  const detail = await (await request.get(`${base}/tasks/${t.num}`)).json();
  expect(detail.body).toContain("decision answered");
  expect(detail.body).toContain("Queue for later");

  await finishTask(request, t.num);
});

test("the answer API rejects a choice that is not an option without mutating", async ({
  request,
}) => {
  const t = await createTaskViaAPI(request, uniqueDesc("dec-badchoice"));
  poseDecision(t.num, Q, OPTS);

  const res = await request.post(`${base}/tasks/${t.num}/answer`, { data: { choice: "Nope" } });
  expect(res.status()).toBe(400);
  // Still a live, deferred decision -- nothing changed.
  const t2 = (await (await request.get(`${base}/tasks`)).json()).tasks.find(
    (x: { num: number }) => x.num === t.num
  );
  expect(t2.has_decision).toBe(true);
  expect(t2.deferred).toBe(true);

  await finishTask(request, t.num);
});

test("a plain resume refuses a live decision, and a second answer 409s", async ({ request }) => {
  const t = await createTaskViaAPI(request, uniqueDesc("dec-resume-guard"));
  poseDecision(t.num, Q, OPTS);

  // A bare resume must not silently drop the decision.
  const resume = await request.post(`${base}/tasks/${t.num}/resume`);
  expect(resume.status()).toBe(409);

  // Answer it, then a second answer has nothing live to resolve.
  expect((await request.post(`${base}/tasks/${t.num}/answer`, { data: { choice: "Retry inline" } })).ok()).toBeTruthy();
  const again = await request.post(`${base}/tasks/${t.num}/answer`, { data: { choice: "Retry inline" } });
  expect(again.status()).toBe(409);

  await finishTask(request, t.num);
});
