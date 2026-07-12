import * as fs from "fs";
import * as path from "path";
import { PROJECT, SEEDS, STORE, commitStore, storeIsLocal } from "./helpers";

/**
 * Global teardown: reset the sandbox to its baseline so nothing accumulates.
 *
 * The store API reads and sorts the whole ledger on each board/features render,
 * so debris left across runs steadily slows the suite until it times out
 * (tasks 074, 077). Two sources accumulate:
 *   - features/*.md    -- only ever created by specs, never seeded, and not
 *                         deletable through the API.
 *   - tasks/*.md       -- specs create tasks; many drive them to done, but some
 *                         leave them pending/in-progress, and the done-only
 *                         prune missed those. Every seed title starts "seed: "
 *                         (slug seed-*) and every spec task uses uniqueDesc
 *                         (slug e2e-*), so keep the seeds and drop the rest.
 *   - screenshots/NNN/ -- upload specs (padBody etc.) leave a directory per
 *                         task number; task prune never touched them, so they
 *                         accumulated unbounded. Seeds carry no screenshots,
 *                         so every subdir here is spec debris.
 * Global setup recreates any missing seed by title, so this is self-healing.
 * Removals are committed so the shared tree stays clean; only runs when the
 * store is local, and tolerates a concurrent cleaner.
 */
export default async function globalTeardown(): Promise<void> {
  if (!storeIsLocal()) return;
  const seedSlugs = Object.values(SEEDS).map(slugify);
  const isSeed = (name: string) => seedSlugs.some((s) => name.includes(s));
  pruneDir(path.join(STORE, PROJECT, "features"), () => true, `${PROJECT}/features`);
  pruneDir(path.join(STORE, PROJECT, "tasks"), (name) => !isSeed(name), `${PROJECT}/tasks`);
  pruneScreenshots();
}

/** Remove every screenshot subdir (all are spec debris; seeds have none) and commit. */
function pruneScreenshots(): void {
  const dir = path.join(STORE, PROJECT, "screenshots");
  if (!fs.existsSync(dir)) return;
  const subs = fs
    .readdirSync(dir)
    .filter((n) => fs.statSync(path.join(dir, n)).isDirectory());
  if (!subs.length) return;
  for (const n of subs) fs.rmSync(path.join(dir, n), { recursive: true, force: true });
  commitStore(`${PROJECT}/screenshots`, `chore(${PROJECT}): prune e2e ${PROJECT}/screenshots fixtures`);
}

/** Mirror the Go store Slugify: lowercase, runs of non-alphanumerics -> one dash. */
function slugify(desc: string): string {
  return desc
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

/** Remove *.md files in dir that `drop` selects, and commit under its rel path. */
function pruneDir(dir: string, drop: (name: string) => boolean, rel: string): void {
  if (!fs.existsSync(dir)) return;
  let removed = 0;
  for (const name of fs.readdirSync(dir)) {
    if (name.endsWith(".md") && drop(name)) {
      fs.rmSync(path.join(dir, name));
      removed++;
    }
  }
  if (!removed) return;
  commitStore(rel, `chore(${PROJECT}): prune e2e ${rel} fixtures`);
}
