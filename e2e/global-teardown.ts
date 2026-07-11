import { execFileSync } from "child_process";
import * as fs from "fs";
import * as path from "path";
import { PROJECT, STORE, storeIsLocal } from "./helpers";

/**
 * Global teardown: prune the sandbox debris a run leaves behind.
 *
 * Two things accumulate across runs and, left alone, slow every board/features
 * render (the API reads and sorts the whole ledger on each load) until the
 * suite drifts into timeouts (tasks 074, 077):
 *   - features/*.md    -- specs are only ever created by specs, never seeded,
 *                         and cannot be deleted through the API.
 *   - tasks/*.done.md  -- every mutation spec drives its tasks to done, and
 *                         nothing reclaims them. Pending / in-progress seeds are
 *                         kept; global setup recreates the single done seed by
 *                         title on the next run.
 * Removals are committed so the shared tree stays clean; only runs when the
 * store is local to the runner, and tolerates a concurrent cleaner.
 */
export default async function globalTeardown(): Promise<void> {
  if (!storeIsLocal()) return;
  pruneDir(path.join(STORE, PROJECT, "features"), (n) => n.endsWith(".md"), `${PROJECT}/features`);
  pruneDir(path.join(STORE, PROJECT, "tasks"), (n) => n.endsWith(".done.md"), `${PROJECT}/tasks`);
}

/** Remove matching files in dir and commit the removal under its rel path. */
function pruneDir(dir: string, match: (name: string) => boolean, rel: string): void {
  if (!fs.existsSync(dir)) return;
  let removed = 0;
  for (const name of fs.readdirSync(dir)) {
    if (match(name)) {
      fs.rmSync(path.join(dir, name));
      removed++;
    }
  }
  if (!removed) return;
  execFileSync("git", ["-C", STORE, "add", "-A", "--", rel]);
  try {
    execFileSync("git", ["-C", STORE, "commit", "-q", "-m", `chore(${PROJECT}): prune e2e ${rel} fixtures`, "--", rel]);
  } catch {
    // Nothing to commit (a concurrent teardown already pruned) -- fine.
  }
}
