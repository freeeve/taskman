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
    // Mutations are fully serialized, so a lost race is a clean 409 -- never
    // a 500 (task 041) -- and the winners are 200.
    expect([200, 409], `unexpected status ${res.status()}`).toContain(res.status());
    const body = await res.json().catch(() => ({}));
    if (body.error) {
      // And the os.LinkError message never carries the absolute store path
      // (task 039).
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

  // The race leaves the task in a nondeterministic status (a "done" may have
  // won), so mark it done best-effort -- a 409 "already done" is fine here
  // and must not fail the cleanup.
  await request.post(`${base}/tasks/${t.num}/status`, { data: { status: "done" } });
});

test("concurrent ship/unship at one feature never 500s, leaves one file, commits", async ({
  request,
}) => {
  test.skip(!storeIsLocal(), "store is not local to the test runner");

  const create = await request.post(`${base}/features`, {
    data: { description: uniqueDesc("ship-race") },
  });
  expect(create.status()).toBe(201);
  const slug = (await create.json()).slug as string;

  // Race done and reopen at one feature. Both rename slug.md <-> slug.done.md
  // behind the same server-wide lock, so the losers get a clean 409 (already
  // in that state, or SetDone's refusing-to-overwrite guard) -- never a 500,
  // and never a leaked store path.
  const routes = ["done", "reopen", "done", "reopen", "done", "reopen", "done"];
  const responses = await Promise.all(routes.map((rt) => request.post(`${base}/features/${slug}/${rt}`)));
  for (const res of responses) {
    expect([200, 409], `unexpected status ${res.status()}`).toContain(res.status());
    const body = await res.json().catch(() => ({}));
    if (body.error) expect(body.error, `leaked path: ${body.error}`).not.toMatch(/\/Users\/|\.taskman\//);
  }

  // Exactly one of the two file forms survives -- never both, never neither.
  const active = fs.existsSync(path.join(FEATURES_DIR, `${slug}.md`));
  const shipped = fs.existsSync(path.join(FEATURES_DIR, `${slug}.done.md`));
  expect(active !== shipped, `active=${active} shipped=${shipped}`).toBe(true);

  // And nothing for this feature is left uncommitted in the store tree.
  const uncommitted = featuresStatus().filter((line) => line.includes(slug));
  expect(uncommitted, `uncommitted after ship/unship race: ${uncommitted.join(", ")}`).toEqual([]);

  // Clean up: remove whichever file form remains and commit.
  for (const name of [`${slug}.md`, `${slug}.done.md`]) {
    const p = path.join(FEATURES_DIR, name);
    if (fs.existsSync(p)) fs.rmSync(p);
  }
  if (featuresStatus().length) {
    execFileSync("git", ["-C", STORE, "add", "-A", "--", `${PROJECT}/features`]);
    execFileSync("git", [
      "-C",
      STORE,
      "commit",
      "-q",
      "-m",
      `chore(${PROJECT}): clean up ship/unship race feature`,
      "--",
      `${PROJECT}/features`,
    ]);
  }
});

test("concurrent same-base body edits keep exactly one winner -- no lost update under a race (task 115)", async ({
  request,
}) => {
  // 115 is verified sequentially elsewhere; this locks it under a TRUE race.
  // Many editors load the same body and save at once carrying the same base
  // etag. Serialized behind the store lock, exactly one write still matches the
  // base and wins (200); every other now sees a changed etag and gets a clean
  // 409. A second 200 would mean a TOCTOU in the check -> a silent lost update.
  const editors = 6;
  const t = await createTaskViaAPI(request, uniqueDesc("edit-race"));
  const loaded = await (await request.get(`${base}/tasks/${t.num}`)).json();

  const statuses = await Promise.all(
    Array.from({ length: editors }, (_, i) =>
      request
        .put(`${base}/tasks/${t.num}`, {
          data: { body: `${loaded.body}\n\nEDITOR-${i}\n`, base: loaded.etag },
        })
        .then((r) => r.status())
    )
  );
  expect(statuses.filter((s) => s === 200), `statuses: ${statuses}`).toHaveLength(1);
  expect(statuses.filter((s) => s === 409)).toHaveLength(editors - 1);
  expect(statuses.every((s) => s === 200 || s === 409)).toBe(true);

  // Exactly one editor's content survived -- no interleaved or lost write.
  const body = (await (await request.get(`${base}/tasks/${t.num}`)).json()).body;
  const survivors = Array.from({ length: editors }, (_, i) => i).filter((i) =>
    body.includes(`EDITOR-${i}`)
  );
  expect(survivors, `surviving editors: ${survivors}`).toHaveLength(1);

  await finishTask(request, t.num);
});
