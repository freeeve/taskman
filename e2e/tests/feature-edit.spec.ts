import { test, expect } from "@playwright/test";
import {
  BASE_URL,
  PROJECT,
  createTaskViaAPI,
  finishTask,
  gotoBoard,
  storeIsLocal,
  uniqueDesc,
} from "../helpers";

/**
 * Feature spec-body editing (task 100): the feature card's "edit" button opens
 * the raw markdown in the task editor's textarea; save PUTs the whole body back
 * (`PUT /features/{slug}`), one scoped commit, and re-renders. The Tasks: line
 * is part of the body, so an edit must preserve linked-task chips. Editing
 * writes the store file, so these gate on a local store.
 */

const base = `${BASE_URL}/api/projects/${PROJECT}`;

test.skip(() => !storeIsLocal(), "store is not local to the test runner");

async function makeFeature(request: import("@playwright/test").APIRequestContext, tag: string) {
  const res = await request.post(`${base}/features`, { data: { description: uniqueDesc(tag) } });
  expect(res.status()).toBe(201);
  return (await res.json()).slug as string;
}

test("editing a feature body persists and re-renders, and keeps its linked-task chips", async ({
  page,
  request,
}) => {
  const t = await createTaskViaAPI(request, uniqueDesc("fedit-task"));
  const slug = await makeFeature(request, "fedit");
  expect((await request.put(`${base}/features/${slug}/tasks`, { data: { tasks: [t.num] } })).ok()).toBeTruthy();

  await gotoBoard(page);
  await page.locator("#tab-features").click();
  const card = page.locator(`.feature-card[data-slug="${slug}"]`);
  await expect(card.locator("button.chip", { hasText: String(t.num).padStart(3, "0") })).toBeVisible();

  // Open the editor; the textarea holds the raw markdown, including Tasks:.
  await card.locator("button", { hasText: "edit" }).click();
  const ta = page.locator("#task-dialog #edit-body");
  await expect(ta).toBeVisible();
  const raw = await ta.inputValue();
  expect(raw).toMatch(/Tasks:/);

  // Append a marker heading and save.
  const marker = `edited-marker-${Date.now()}`;
  await ta.fill(raw + `\n\n## ${marker}\n\nnew spec content.\n`);
  await page.locator("#dialog-actions button", { hasText: "save" }).click();
  await expect(page.locator("#task-dialog")).toBeHidden();

  // The new content renders in the spec panel...
  await card.locator("details summary").click();
  await expect(card.locator(".md")).toContainText(marker);
  // ...and the linked-task chip survived (Tasks: line was preserved).
  await expect(card.locator("button.chip", { hasText: String(t.num).padStart(3, "0") })).toBeVisible();

  // And it persisted server-side: the raw body kept the marker, and the
  // feature list still reports the link (the Tasks: line survived the rewrite).
  const detail = await (await request.get(`${base}/features/${slug}`)).json();
  expect(detail.body).toContain(marker);
  const listed = (await (await request.get(`${base}/features`)).json()).find(
    (x: { slug: string }) => x.slug === slug
  );
  expect(listed.tasks.map((x: { num: number }) => x.num)).toContain(t.num);

  await request.delete(`${base}/features/${slug}`);
  await finishTask(request, t.num);
});

test("editing a feature body prompts before a backdrop click discards it (task 101)", async ({
  page,
  request,
}) => {
  const slug = await makeFeature(request, "fedit-dirty");
  await gotoBoard(page);
  await page.locator("#tab-features").click();
  const card = page.locator(`.feature-card[data-slug="${slug}"]`);
  await card.locator("button", { hasText: "edit" }).click();
  const ta = page.locator("#task-dialog #edit-body");
  await expect(ta).toBeVisible();
  const marker = `dirty-${Date.now()}`;
  await ta.fill((await ta.inputValue()) + `\n${marker}\n`);

  // A backdrop click with unsaved edits fires a discard confirm; dismissing it
  // keeps the dialog open with the text intact.
  let message = "";
  page.once("dialog", (d) => {
    message = d.message();
    d.dismiss();
  });
  await page.mouse.click(5, 5);
  expect(message).toContain("Discard");
  await expect(page.locator("#task-dialog")).toBeVisible();
  expect(await ta.inputValue()).toContain(marker);

  // Accepting the confirm closes and discards.
  page.once("dialog", (d) => d.accept());
  await page.mouse.click(5, 5);
  await expect(page.locator("#task-dialog")).toBeHidden();

  await request.delete(`${base}/features/${slug}`);
});

test("an edited feature body's raw HTML is neutralized, not rendered live", async ({ request }) => {
  const slug = await makeFeature(request, "fedit-xss");
  const body = `# spec\n\nBefore <img src=x onerror="window.__x=1"> after\n`;
  expect((await request.put(`${base}/features/${slug}`, { data: { body } })).ok()).toBeTruthy();

  const detail = await (await request.get(`${base}/features/${slug}`)).json();
  // Same renderBody path as everywhere: raw HTML is dropped, not emitted live.
  expect(detail.html).not.toContain("<img");
  expect(detail.html).toContain("raw HTML omitted");

  await request.delete(`${base}/features/${slug}`);
});

test("the feature edit API rejects an empty body and an unknown slug", async ({ request }) => {
  const slug = await makeFeature(request, "fedit-guard");
  const empty = await request.put(`${base}/features/${slug}`, { data: { body: "   " } });
  expect(empty.status()).toBe(400);

  const missing = await request.put(`${base}/features/no-such-feature-xyz`, { data: { body: "x" } });
  expect(missing.status()).toBe(404);
  expect((await missing.json()).error).toBeTruthy();

  await request.delete(`${base}/features/${slug}`);
});
