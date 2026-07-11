import { test, expect } from "@playwright/test";
import { execFileSync } from "child_process";
import * as fs from "fs";
import * as path from "path";
import {
  BASE_URL,
  FEATURES_DIR,
  PROJECT,
  STORE,
  createTaskViaAPI,
  finishTask,
  storeIsLocal,
  uniqueDesc,
} from "../helpers";

/**
 * The store is a git repo and every web mutation must land as a commit --
 * taskman's audit-trail contract. The add+commit pair is two git calls, so
 * concurrent handler goroutines once raced the shared index and left a
 * mutation staged or untracked while its request still returned 2xx (task
 * 035). This fires a burst of concurrent creates and asserts nothing is
 * left uncommitted. It inspects and tidies the store on disk, so it only
 * runs when the store is local to the runner.
 */

const base = `${BASE_URL}/api/projects/${PROJECT}`;
const N = 12;

/** Porcelain status lines for the project's features dir. */
function featuresStatus(): string[] {
  const out = execFileSync("git", ["-C", STORE, "status", "--porcelain", "--", `${PROJECT}/features`], {
    encoding: "utf8",
  });
  return out.split("\n").filter(Boolean);
}

test("a burst of concurrent feature creates are all committed, tree stays clean", async ({
  request,
}) => {
  test.skip(!storeIsLocal(), "store is not local to the test runner");

  const tag = `conc-${Date.now()}`;
  const responses = await Promise.all(
    Array.from({ length: N }, (_, i) =>
      request.post(`${base}/features`, { data: { description: `${uniqueDesc(tag)} ${i}` } })
    )
  );

  // Every create must report success...
  for (const res of responses) expect(res.status()).toBe(201);
  const slugs = await Promise.all(responses.map(async (r) => (await r.json()).slug as string));

  // ...and none may be left uncommitted in the store working tree.
  const uncommitted = featuresStatus().filter((line) => slugs.some((s) => line.includes(s)));
  expect(uncommitted, `uncommitted after ${N} concurrent creates: ${uncommitted.join(", ")}`).toEqual(
    []
  );

  // The features can't be deleted through the API; remove the files and
  // commit the deletions so the shared store stays clean for other work.
  for (const slug of slugs) {
    const file = path.join(FEATURES_DIR, `${slug}.md`);
    if (fs.existsSync(file)) fs.rmSync(file);
  }
  if (featuresStatus().length) {
    execFileSync("git", ["-C", STORE, "add", "-A", "--", `${PROJECT}/features`]);
    execFileSync("git", [
      "-C",
      STORE,
      "commit",
      "-q",
      "-m",
      `chore(${PROJECT}): clean up concurrency regression features`,
      "--",
      `${PROJECT}/features`,
    ]);
  }
});

test("concurrent same-task status changes never leak the store path and leave one file", async ({
  request,
}) => {
  test.skip(!storeIsLocal(), "store is not local to the test runner");
  const t = await createTaskViaAPI(request, uniqueDesc("same-task"));

  // Race several status changes at one task: the losers' os.Rename fails
  // (the winner already moved the file). Those errors are os.LinkErrors --
  // their message must not contain the absolute store path (task 039).
  const targets = ["in-progress", "done", "pending", "done", "in-progress", "pending", "done"];
  const responses = await Promise.all(
    targets.map((status) => request.post(`${base}/tasks/${t.num}/status`, { data: { status } }))
  );
  for (const res of responses) {
    const body = await res.json().catch(() => ({}));
    if (body.error) {
      expect(body.error, `leaked path: ${body.error}`).not.toMatch(/\/Users\/|\.taskman\//);
    }
  }

  // The race must leave exactly one file for the task -- no duplicate, no
  // zero -- and a valid status.
  const tasksDir = path.join(STORE, PROJECT, "tasks");
  const pad = String(t.num).padStart(3, "0");
  const files = fs
    .readdirSync(tasksDir)
    .filter((f) => f.startsWith(`${pad}_`) || f.startsWith(`${pad}-`));
  expect(files, `task files after race: ${files.join(", ")}`).toHaveLength(1);

  await finishTask(request, t.num);
});
