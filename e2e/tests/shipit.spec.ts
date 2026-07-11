import { test, expect, type Page } from "@playwright/test";
import { execFileSync } from "child_process";
import * as fs from "fs";
import * as path from "path";
import { BASE_URL, FEATURES_DIR, PROJECT, STORE, gotoBoard, storeIsLocal, uniqueDesc } from "../helpers";

/**
 * Ship / unship lifecycle for features (task 049). Shipping is now confirmed
 * and reversible: POST features/{slug}/done and .../reopen round-trip the
 * slug.md <-> slug.done.md rename, the UI guards ship it with confirm() and
 * offers an unship button on shipped cards. These specs create features (which
 * cannot be deleted via the API) so they run against the sandbox and clean up
 * their files on disk.
 */

const base = `${BASE_URL}/api/projects/${PROJECT}`;

test.skip(() => !storeIsLocal(), "store is not local to the test runner");

/** Create a feature via the API and return its slug. */
async function createFeature(page: Page, description: string): Promise<string> {
  const res = await page.request.post(`${base}/features`, { data: { description } });
  expect(res.status()).toBe(201);
  return (await res.json()).slug;
}

/** Switch to the features tab and wait for its list to load. */
async function openFeaturesTab(page: Page): Promise<void> {
  await Promise.all([
    page.waitForResponse((r) => r.url().includes(`${PROJECT}/features`)),
    page.locator("#tab-features").click(),
  ]);
}

/** Fetch a feature's wire shape by slug, or undefined. */
async function featureBySlug(page: Page, slug: string) {
  const res = await page.request.get(`${base}/features`);
  expect(res.ok()).toBeTruthy();
  return (await res.json()).find((f: { slug: string }) => f.slug === slug);
}

/** Remove a feature's file (active or shipped) and commit, keeping the sandbox clean. */
function removeFeatureBySlug(slug: string): void {
  for (const name of [`${slug}.done.md`, `${slug}.md`]) {
    const p = path.join(FEATURES_DIR, name);
    if (fs.existsSync(p)) fs.rmSync(p);
  }
  const dirty = execFileSync("git", ["-C", STORE, "status", "--porcelain", "--", `${PROJECT}/features`], {
    encoding: "utf8",
  });
  if (!dirty.trim()) return;
  execFileSync("git", ["-C", STORE, "add", "-A", "--", `${PROJECT}/features`]);
  execFileSync("git", [
    "-C",
    STORE,
    "commit",
    "-q",
    "-m",
    `chore(${PROJECT}): clean up shipit lifecycle feature`,
    "--",
    `${PROJECT}/features`,
  ]);
}

test("API: reopen rejects a feature that is not shipped with a 409", async ({ page }) => {
  const slug = await createFeature(page, uniqueDesc("reopen-active"));

  const res = await page.request.post(`${base}/features/${slug}/reopen`);
  expect(res.status()).toBe(409);
  // The rejected reopen must not leak the store path.
  const body = await res.json().catch(() => ({}));
  if (body.error) expect(body.error).not.toMatch(/\/Users\/|\.taskman\//);
  // ...and the feature stays active.
  expect((await featureBySlug(page, slug)).done).toBe(false);

  removeFeatureBySlug(slug);
});

test("API: done then reopen round-trips the feature's file on disk", async ({ page }) => {
  const slug = await createFeature(page, uniqueDesc("roundtrip"));
  const active = path.join(FEATURES_DIR, `${slug}.md`);
  const shipped = path.join(FEATURES_DIR, `${slug}.done.md`);

  const ship = await page.request.post(`${base}/features/${slug}/done`);
  expect(ship.status()).toBe(200);
  expect((await featureBySlug(page, slug)).done).toBe(true);
  expect(fs.existsSync(shipped)).toBe(true);
  expect(fs.existsSync(active)).toBe(false);

  const reopen = await page.request.post(`${base}/features/${slug}/reopen`);
  expect(reopen.status()).toBe(200);
  expect((await featureBySlug(page, slug)).done).toBe(false);
  expect(fs.existsSync(active)).toBe(true);
  expect(fs.existsSync(shipped)).toBe(false);

  removeFeatureBySlug(slug);
});

test("UI: cancelling the ship-it confirm leaves the feature active", async ({ page }) => {
  const desc = uniqueDesc("ship-cancel");
  const slug = await createFeature(page, desc);
  await gotoBoard(page);
  await openFeaturesTab(page);
  const cardSel = `.feature-card[data-slug="${slug}"]`;
  await expect(page.locator(cardSel)).toBeVisible();

  // Dismiss the confirm(): nothing is posted, the card stays active.
  page.once("dialog", (d) => d.dismiss());
  await page.locator(cardSel).locator("button", { hasText: "ship it" }).click();
  await expect(page.locator(cardSel).locator("button", { hasText: "ship it" })).toBeVisible();
  await expect(page.locator(cardSel).locator(".badge", { hasText: "shipped" })).toHaveCount(0);
  await expect(page.locator(`${cardSel} .feature-slug`)).toHaveText(`${slug}.md`);
  expect((await featureBySlug(page, slug)).done).toBe(false);

  removeFeatureBySlug(slug);
});

test("UI: ship it (confirmed) then unship round-trips the card state", async ({ page }) => {
  const desc = uniqueDesc("ship-unship");
  const slug = await createFeature(page, desc);
  await gotoBoard(page);
  await openFeaturesTab(page);
  const cardSel = `.feature-card[data-slug="${slug}"]`;
  await expect(page.locator(cardSel)).toBeVisible();

  // Confirm the ship: badge, unship button, and .done.md slug appear.
  page.once("dialog", (d) => d.accept());
  await Promise.all([
    page.waitForResponse((r) => r.url().includes(`${PROJECT}/features`) && r.request().method() === "GET"),
    page.locator(cardSel).locator("button", { hasText: "ship it" }).click(),
  ]);
  await expect(page.locator(cardSel).locator(".badge", { hasText: "shipped" })).toBeVisible();
  await expect(page.locator(cardSel).locator("button", { hasText: "unship" })).toBeVisible();
  await expect(page.locator(`${cardSel} .feature-slug`)).toHaveText(`${slug}.done.md`);

  // Unship returns it to active: ship it button back, .md slug back.
  await Promise.all([
    page.waitForResponse((r) => r.url().includes(`${PROJECT}/features`) && r.request().method() === "GET"),
    page.locator(cardSel).locator("button", { hasText: "unship" }).click(),
  ]);
  await expect(page.locator(cardSel).locator("button", { hasText: "ship it" })).toBeVisible();
  await expect(page.locator(cardSel).locator(".badge", { hasText: "shipped" })).toHaveCount(0);
  await expect(page.locator(`${cardSel} .feature-slug`)).toHaveText(`${slug}.md`);

  removeFeatureBySlug(slug);
});
