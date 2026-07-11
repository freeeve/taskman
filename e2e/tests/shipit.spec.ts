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

/** The store's current HEAD commit hash. */
function headCommit(): string {
  return execFileSync("git", ["-C", STORE, "rev-parse", "HEAD"], { encoding: "utf8" }).trim();
}

/** Commits added to the store since `base`, subject + files touched (renames split). */
function commitsSince(base: string): { subject: string; files: string[] }[] {
  const hashes = execFileSync("git", ["-C", STORE, "rev-list", `${base}..HEAD`], { encoding: "utf8" })
    .split("\n")
    .filter(Boolean);
  return hashes.map((h) => ({
    subject: execFileSync("git", ["-C", STORE, "log", "-1", "--format=%s", h], { encoding: "utf8" }).trim(),
    files: execFileSync("git", ["-C", STORE, "show", "--name-only", "--no-renames", "--format=", h], {
      encoding: "utf8",
    })
      .split("\n")
      .filter(Boolean),
  }));
}

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

test("API: re-creating a slug that is already shipped is a clean 409, spec preserved", async ({
  page,
}) => {
  const desc = uniqueDesc("slug-guard");
  const slug = await createFeature(page, desc);
  const shipped = path.join(FEATURES_DIR, `${slug}.done.md`);
  const active = path.join(FEATURES_DIR, `${slug}.md`);

  // Ship it, then capture the shipped spec's bytes.
  expect((await page.request.post(`${base}/features/${slug}/done`)).status()).toBe(200);
  expect(fs.existsSync(shipped)).toBe(true);
  const before = fs.readFileSync(shipped, "utf8");

  // Re-creating the same description would resolve to the shipped slug. It
  // must be refused with a 409 (never a 500 or a silent overwrite that would
  // destroy the shipped spec), and the error must not leak the store path.
  const res = await page.request.post(`${base}/features`, { data: { description: desc } });
  expect(res.status()).toBe(409);
  const err = (await res.json()).error as string;
  expect(err).toContain("already exists");
  expect(err).not.toMatch(/\/Users\/|\.taskman\//);

  // The shipped spec is untouched and no active file was created.
  expect(fs.existsSync(active)).toBe(false);
  expect(fs.readFileSync(shipped, "utf8")).toBe(before);

  removeFeatureBySlug(slug);
});

test("each feature lifecycle mutation lands as exactly one commit scoped to its files", async ({
  page,
}) => {
  // taskman's audit-trail contract: every web mutation is one store commit,
  // touching only that mutation's files. The suite is single-worker, so the
  // commits added during this test are exactly create + ship + unship.
  const before = headCommit();
  const desc = uniqueDesc("commit-trail");
  const slug = await createFeature(page, desc);
  expect((await page.request.post(`${base}/features/${slug}/done`)).status()).toBe(200);
  expect((await page.request.post(`${base}/features/${slug}/reopen`)).status()).toBe(200);

  const commits = commitsSince(before);
  expect(commits.length, `commits: ${commits.map((c) => c.subject).join(" | ")}`).toBe(3);
  const ownFile = new RegExp(`^${PROJECT}/features/${slug}(\\.done)?\\.md$`);
  for (const c of commits) {
    expect(c.files.length, `empty commit "${c.subject}"`).toBeGreaterThan(0);
    for (const f of c.files) {
      expect(f, `stray file in commit "${c.subject}"`).toMatch(ownFile);
    }
    expect(c.subject, `not a scoped semantic message: "${c.subject}"`).toContain(`chore(${PROJECT}):`);
  }

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

test("UI: shipping keeps the open spec panel open as the card moves to the done section", async ({
  page,
}) => {
  const desc = uniqueDesc("ship-panel");
  const slug = await createFeature(page, desc);
  await gotoBoard(page);
  await openFeaturesTab(page);
  const cardSel = `.feature-card[data-slug="${slug}"]`;
  await expect(page.locator(cardSel)).toBeVisible();

  // Open the spec, then ship it. The card relocates from the active partition
  // to the done partition, but renderFeatures preserves open panels by slug --
  // the reading position must survive the move (cf. task 033).
  await page.locator(`${cardSel} summary`).click();
  await expect(page.locator(`${cardSel} details`)).toHaveJSProperty("open", true);

  page.once("dialog", (d) => d.accept());
  await Promise.all([
    page.waitForResponse((r) => r.url().includes(`${PROJECT}/features`) && r.request().method() === "GET"),
    page.locator(cardSel).locator("button", { hasText: "ship it" }).click(),
  ]);
  await expect(page.locator(cardSel)).toHaveClass(/done/);
  await expect(page.locator(`${cardSel} details`)).toHaveJSProperty("open", true);

  removeFeatureBySlug(slug);
});

test("UI: a stale ship-it that 409s still refreshes the card to the shipped state", async ({
  page,
}) => {
  const desc = uniqueDesc("ship-conflict");
  const slug = await createFeature(page, desc);
  await gotoBoard(page);
  await openFeaturesTab(page);
  const cardSel = `.feature-card[data-slug="${slug}"]`;
  await expect(page.locator(cardSel).locator("button", { hasText: "ship it" })).toBeVisible();

  // Another client ships it out-of-band: the open tab's card is now stale.
  expect((await page.request.post(`${base}/features/${slug}/done`)).status()).toBe(200);

  // Click the stale "ship it": confirm() then a 409 "already done" alert. The
  // fix (task 068) refreshes regardless, so the card self-corrects to shipped
  // without a manual tab switch -- accept both dialogs.
  page.on("dialog", (d) => d.accept());
  await Promise.all([
    page.waitForResponse((r) => r.url().includes(`${PROJECT}/features`) && r.request().method() === "GET"),
    page.locator(cardSel).locator("button", { hasText: "ship it" }).click(),
  ]);
  await expect(page.locator(cardSel).locator(".badge", { hasText: "shipped" })).toBeVisible();
  await expect(page.locator(cardSel).locator("button", { hasText: "unship" })).toBeVisible();
  await expect(page.locator(cardSel).locator("button", { hasText: "ship it" })).toHaveCount(0);
  await expect(page.locator(`${cardSel} .feature-slug`)).toHaveText(`${slug}.done.md`);

  removeFeatureBySlug(slug);
});
