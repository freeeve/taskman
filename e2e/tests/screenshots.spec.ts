import { test, expect } from "@playwright/test";
import {
  BASE_URL,
  PROJECT,
  TINY_PNG,
  createTaskViaAPI,
  finishTask,
  gotoBoard,
  openCard,
  uniqueDesc,
} from "../helpers";

/**
 * Screenshot tests: upload via the API contract, attach via paste and drop
 * in the task dialog, and the /shots/ serving guards.
 */

const base = `${BASE_URL}/api/projects/${PROJECT}`;

test("uploading a png attaches it to the task and serves it back", async ({ request }) => {
  const t = await createTaskViaAPI(request, uniqueDesc("shot-api"));

  const res = await request.post(`${base}/tasks/${t.num}/screenshots`, {
    multipart: {
      file: { name: "shot.png", mimeType: "image/png", buffer: TINY_PNG },
    },
  });
  expect(res.status()).toBe(201);
  const { path } = await res.json();
  expect(path).toMatch(new RegExp(`^screenshots/${String(t.num).padStart(3, "0")}/.+\\.png$`));

  const shot = await request.get(`${BASE_URL}/shots/${PROJECT}/${t.num}/${path.split("/").pop()}`);
  expect(shot.ok()).toBeTruthy();
  expect(shot.headers()["content-type"]).toContain("image/png");

  const detail = await request.get(`${base}/tasks/${t.num}`);
  const { html } = await detail.json();
  expect(html).toContain(`src="/shots/${PROJECT}/`);

  await finishTask(request, t.num);
});

test("dropping an image on the open dialog uploads and renders it inline", async ({ page }) => {
  const t = await createTaskViaAPI(page.request, uniqueDesc("shot-drop"));
  await gotoBoard(page);
  await openCard(page, t.num);

  await page.evaluate((b64) => {
    const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
    const dt = new DataTransfer();
    dt.items.add(new File([bytes], "drop.png", { type: "image/png" }));
    document
      .querySelector("#task-dialog")!
      .dispatchEvent(new DragEvent("drop", { dataTransfer: dt, bubbles: true, cancelable: true }));
  }, TINY_PNG.toString("base64"));

  await expect(page.locator('#dialog-body img[src^="/shots/"]')).toBeVisible();
  await finishTask(page.request, t.num);
});

test("pasting an image while the dialog is open uploads it", async ({ page }) => {
  const t = await createTaskViaAPI(page.request, uniqueDesc("shot-paste"));
  await gotoBoard(page);
  await openCard(page, t.num);

  await page.evaluate((b64) => {
    const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
    const dt = new DataTransfer();
    dt.items.add(new File([bytes], "paste.png", { type: "image/png" }));
    window.dispatchEvent(
      new ClipboardEvent("paste", { clipboardData: dt, bubbles: true, cancelable: true })
    );
  }, TINY_PNG.toString("base64"));

  await expect(page.locator('#dialog-body img[src^="/shots/"]')).toBeVisible();
  await finishTask(page.request, t.num);
});

test("rejects a non-image upload", async ({ request }) => {
  const t = await createTaskViaAPI(request, uniqueDesc("shot-reject"));
  const res = await request.post(`${base}/tasks/${t.num}/screenshots`, {
    multipart: {
      file: { name: "notes.txt", mimeType: "text/plain", buffer: Buffer.from("not an image") },
    },
  });
  expect(res.status()).toBe(400);
  expect((await res.json()).error).toContain("unsupported");
  await finishTask(request, t.num);
});

test("the /shots/ route refuses dotfiles and unknown files", async ({ request }) => {
  const dot = await request.get(`${BASE_URL}/shots/${PROJECT}/1/.hidden`);
  expect(dot.status()).toBe(404);
  const missing = await request.get(`${BASE_URL}/shots/${PROJECT}/999/nope.png`);
  expect(missing.status()).toBe(404);
});
