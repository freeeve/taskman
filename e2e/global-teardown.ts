import { execFileSync } from "child_process";
import * as fs from "fs";
import * as path from "path";
import { FEATURES_DIR, PROJECT, STORE, storeIsLocal } from "./helpers";

/**
 * Global teardown: prune the sandbox's feature files after the run.
 *
 * Every feature in the sandbox is created by a spec -- global setup seeds
 * tasks, never features -- so left alone they accumulate across runs (hundreds
 * of files), which slows every features-tab test and, under concurrent store
 * activity, pushes the suite toward timeouts (task 074). Removing them keeps
 * each run starting from a lean sandbox; specs that need features create their
 * own. Only runs when the store is local to the runner, and commits the
 * removal so the shared tree stays clean (tolerating a concurrent cleaner).
 */
export default async function globalTeardown(): Promise<void> {
  if (!storeIsLocal() || !fs.existsSync(FEATURES_DIR)) return;

  let removed = 0;
  for (const name of fs.readdirSync(FEATURES_DIR)) {
    if (name.endsWith(".md")) {
      fs.rmSync(path.join(FEATURES_DIR, name));
      removed++;
    }
  }
  if (!removed) return;

  const rel = `${PROJECT}/features`;
  execFileSync("git", ["-C", STORE, "add", "-A", "--", rel]);
  try {
    execFileSync("git", ["-C", STORE, "commit", "-q", "-m", `chore(${PROJECT}): prune e2e feature fixtures`, "--", rel]);
  } catch {
    // Nothing to commit (a concurrent teardown already pruned) -- fine.
  }
}
