import { expect, type APIRequestContext, type Page } from "@playwright/test";
import { execFileSync } from "child_process";
import * as fs from "fs";
import * as os from "os";
import * as path from "path";

/** Base URL of the running `taskman serve` instance under test. */
export const BASE_URL = process.env.E2E_BASE_URL || "http://localhost:8311";

/**
 * Store project the suite runs against. Mutation tests create tasks and
 * auto-commit into the store, so this must be a sandbox project, never a
 * real ledger: `mkdir -p ~/.taskman/e2e-sandbox/tasks` to create it.
 */
export const PROJECT = process.env.E2E_PROJECT || "e2e-sandbox";

/**
 * Store root on disk. Most of the suite is HTTP-only, but a few features
 * specs must link tasks to a feature -- the only way is editing the
 * feature's "Tasks:" line, which the API does not expose. Those specs edit
 * the file directly and so only run when the store is local to the runner
 * (the default: the server on :8311 serves this same ~/.taskman).
 */
export const STORE =
  process.env.E2E_STORE || process.env.TASKMAN_HOME || path.join(os.homedir(), ".taskman");

/** The suite project's features directory on disk. */
export const FEATURES_DIR = path.join(STORE, PROJECT, "features");

/** True when the store is reachable on this filesystem (gates fs-based specs). */
export function storeIsLocal(): boolean {
  return fs.existsSync(path.join(STORE, PROJECT));
}

/** Current store HEAD commit hash; a baseline for commitsSince(). */
export function headCommit(): string {
  return execFileSync("git", ["-C", STORE, "rev-parse", "HEAD"], { encoding: "utf8" }).trim();
}

/**
 * Commits made to the store since `base`, newest first, each with its subject
 * and the files it touched. The store is multi-writer (other projects commit
 * concurrently), so callers filter by the files they care about rather than
 * assuming a raw count.
 */
export function commitsSince(base: string): { subject: string; files: string[] }[] {
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

/**
 * Commit a feature file into the store so a direct-to-disk edit does not
 * leave the shared working tree dirty. The API commits its own mutations;
 * these fs helpers edit the "Tasks:" line / body that the API does not
 * expose, so they must tidy up after themselves.
 */
function commitFeatureFile(slug: string): void {
  const rel = `${PROJECT}/features/${slug}.md`;
  execFileSync("git", ["-C", STORE, "add", "--", rel]);
  execFileSync("git", ["-C", STORE, "commit", "-q", "-m", `chore(${PROJECT}): e2e edit ${slug}`, "--", rel]);
}

/**
 * Set a feature's linked task numbers by rewriting its "Tasks:" line on
 * disk, then commit so the store tree stays clean. Only valid when
 * storeIsLocal(). Callers reload the features view to see the new chips.
 */
export function linkTasksToFeature(slug: string, nums: number[]): void {
  const file = path.join(FEATURES_DIR, `${slug}.md`);
  const body = fs.readFileSync(file, "utf8");
  fs.writeFileSync(file, body.replace(/^Tasks:.*$/m, `Tasks: ${nums.join(", ")}`));
  commitFeatureFile(slug);
}

/**
 * Append markdown to a feature's body on disk, then commit so the store
 * tree stays clean. Only valid when storeIsLocal().
 */
export function appendFeatureBody(slug: string, markdown: string): void {
  fs.appendFileSync(path.join(FEATURES_DIR, `${slug}.md`), markdown);
  commitFeatureFile(slug);
}

/**
 * Append markdown to a task's body on disk, then commit so the store tree
 * stays clean. The API cannot edit task bodies, so specs that need rich body
 * content (tables, links) write the file directly. Only valid when
 * storeIsLocal(); pass the task's `file` from its wire shape.
 */
export function appendTaskBody(file: string, markdown: string): void {
  const rel = `${PROJECT}/tasks/${file}`;
  fs.appendFileSync(path.join(STORE, PROJECT, "tasks", file), markdown);
  execFileSync("git", ["-C", STORE, "add", "--", rel]);
  execFileSync("git", ["-C", STORE, "commit", "-q", "-m", `chore(${PROJECT}): e2e task body edit`, "--", rel]);
}

/**
 * Write a raw task file directly into the store and commit it. Used to seed a
 * state the API refuses to create -- notably a second file sharing an existing
 * number, to exercise the board's duplicate-number resilience. The normal
 * allocation lock prevents dupes, so they can only be forged on disk. Returns
 * the file name. Only valid when storeIsLocal().
 */
export function writeRawTaskFile(fileName: string, body: string): string {
  const rel = `${PROJECT}/tasks/${fileName}`;
  fs.writeFileSync(path.join(STORE, PROJECT, "tasks", fileName), body);
  execFileSync("git", ["-C", STORE, "add", "--", rel]);
  execFileSync("git", ["-C", STORE, "commit", "-q", "-m", `chore(${PROJECT}): e2e seed ${fileName}`, "--", rel]);
  return fileName;
}

/** Delete a store task file and commit the removal. Only valid when storeIsLocal(). */
export function removeRawTaskFile(fileName: string): void {
  const rel = `${PROJECT}/tasks/${fileName}`;
  fs.rmSync(path.join(STORE, PROJECT, "tasks", fileName), { force: true });
  execFileSync("git", ["-C", STORE, "add", "-A", "--", rel]);
  try {
    execFileSync("git", ["-C", STORE, "commit", "-q", "-m", `chore(${PROJECT}): e2e remove ${fileName}`, "--", rel]);
  } catch {
    // Nothing to commit (already gone) -- fine.
  }
}

/**
 * Path to a taskman binary able to pose structured decisions. A decision is
 * posed only through the CLI (`defer -question -option ...`); the web API
 * defers with a reason only. Prefer $TASKMAN_BIN, else the repo's freshly
 * built `bin/taskman` (what `serve` runs), else `taskman` on PATH.
 */
export function taskmanBin(): string {
  if (process.env.TASKMAN_BIN) return process.env.TASKMAN_BIN;
  const repoBin = path.join(__dirname, "..", "bin", "taskman");
  return fs.existsSync(repoBin) ? repoBin : "taskman";
}

/**
 * True when the resolved taskman binary understands `defer -question`, so a
 * decision spec can pose its precondition. Gates specs on a current CLI (an
 * installed binary predating the decisions feature lacks the flag).
 */
export function decisionPoseSupported(): boolean {
  try {
    return execFileSync(taskmanBin(), ["defer", "-h"], { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }).includes(
      "question"
    );
  } catch (e) {
    // `defer -h` exits non-zero after printing usage to stderr; inspect it.
    const out = (e as { stderr?: string; stdout?: string }).stderr ?? (e as { stdout?: string }).stdout ?? "";
    return out.includes("question");
  }
}

/**
 * Pose a structured decision on a task via the CLI, deferring it. Options are
 * `"Label::explanation"` pairs. Only valid when storeIsLocal() and
 * decisionPoseSupported(); the mutation auto-commits into the store.
 */
export function poseDecision(num: number, question: string, options: string[]): void {
  const args = ["defer", "-question", question];
  for (const o of options) args.push("-option", o);
  args.push("-p", PROJECT, String(num));
  execFileSync(taskmanBin(), args, { encoding: "utf8" });
}

/** Title prefix of the baseline fixture tasks created by global setup. */
export const SEED_PREFIX = "seed: ";

/** Titles of the baseline fixtures; global setup guarantees these exist. */
export const SEEDS = {
  pendingWeb: "seed: pending alpha",
  pendingE2E: "seed: pending beta",
  pendingBare: "seed: pending gamma",
  inProgress: "seed: in progress delta",
  done: "seed: done epsilon",
  deferred: "seed: deferred zeta",
} as const;

/** Wire shape of one task as served by the JSON API. */
export interface TaskJSON {
  num: number;
  lane: string;
  slug: string;
  status: string;
  deferred: boolean;
  file: string;
  title: string;
}

/** GET the project's full ledger: tasks in priority order, order, lanes. */
export async function getTasks(
  request: APIRequestContext
): Promise<{ tasks: TaskJSON[]; order: number[]; lanes: string[] }> {
  const res = await request.get(`${BASE_URL}/api/projects/${PROJECT}/tasks`);
  expect(res.ok()).toBeTruthy();
  return res.json();
}

/** Find a task by exact title, failing the test if it is missing. */
export async function taskByTitle(
  request: APIRequestContext,
  title: string
): Promise<TaskJSON> {
  const { tasks } = await getTasks(request);
  const t = tasks.find((x) => x.title === title);
  expect(t, `task titled ${JSON.stringify(title)} should exist`).toBeTruthy();
  return t!;
}

/** POST a new task via the API and return its wire shape. */
export async function createTaskViaAPI(
  request: APIRequestContext,
  description: string,
  lane = ""
): Promise<TaskJSON> {
  const res = await request.post(`${BASE_URL}/api/projects/${PROJECT}/tasks`, {
    data: { description, lane },
  });
  expect(res.status()).toBe(201);
  return res.json();
}

/** Move a task to pending / in-progress / done via the API. */
export async function setStatusViaAPI(
  request: APIRequestContext,
  num: number,
  status: "pending" | "in-progress" | "done"
): Promise<void> {
  const res = await request.post(
    `${BASE_URL}/api/projects/${PROJECT}/tasks/${num}/status`,
    { data: { status } }
  );
  expect(res.ok()).toBeTruthy();
}

/** Mark a test-created task done so it leaves the pending/in-progress columns. */
export async function finishTask(
  request: APIRequestContext,
  num: number
): Promise<void> {
  await setStatusViaAPI(request, num, "done");
}

/** Unique task description so runs never collide on slug or title. */
export function uniqueDesc(label: string): string {
  return `e2e ${label} ${Date.now()}-${Math.floor(Math.random() * 1e6)}`;
}

/**
 * Select a project through the searchable picker: open the panel, type the
 * exact name to filter to a single match, and click it. The native #project
 * select is a hidden state holder, so tests drive the picker like a user.
 */
export async function selectProjectViaPicker(page: Page, project: string): Promise<void> {
  await page.locator("#project-button").click();
  await expect(page.locator("#picker-panel")).toBeVisible();
  await page.locator("#picker-search").fill(project);
  await page.locator("#picker-list li", { hasText: project }).first().click();
  await expect(page.locator("#picker-panel")).toBeHidden();
}

/**
 * Open the board and switch it to the suite's project through the picker,
 * waiting for the ledger fetch that the switch triggers to land. If the
 * board already loaded on the target project (persisted selection), no
 * switch is needed.
 */
export async function gotoBoard(page: Page): Promise<void> {
  await page.goto("/");
  await expect(page.locator("#project option")).not.toHaveCount(0);
  if ((await page.locator("#project").inputValue()) !== PROJECT) {
    await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes(`/api/projects/${PROJECT}/tasks`) && r.request().method() === "GET"
      ),
      selectProjectViaPicker(page, PROJECT),
    ]);
  }
  await expect(page.locator(".column")).toHaveCount(3);
}

/** Locator for a task's card on the board. */
export function card(page: Page, num: number) {
  return page.locator(`.card[data-num="${num}"]`);
}

/**
 * Create a task through the "+ task" button, answering the prompt() dialog,
 * and return its wire shape once the board has reloaded.
 */
export async function createTaskViaUI(
  page: Page,
  description: string
): Promise<TaskJSON> {
  page.once("dialog", (d) => d.accept(description));
  await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes(`/api/projects/${PROJECT}/tasks`) && r.request().method() === "GET"
    ),
    page.locator("#new-task").click(),
  ]);
  const t = await taskByTitle(page.request, description);
  await expect(card(page, t.num)).toBeVisible();
  return t;
}

/** Open a card's detail dialog and wait for it to render. */
export async function openCard(page: Page, num: number): Promise<void> {
  await card(page, num).click();
  await expect(page.locator("#task-dialog")).toBeVisible();
  await expect(page.locator("#dialog-file")).not.toBeEmpty();
}

/**
 * Click a lifecycle action button in the open task dialog and wait for the
 * board reload the mutation triggers.
 */
export async function dialogAction(page: Page, label: string): Promise<void> {
  await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes(`/api/projects/${PROJECT}/tasks`) && r.request().method() === "GET"
    ),
    page.locator("#dialog-actions button", { hasText: label }).click(),
  ]);
}

/**
 * Simulate an HTML5 drag of a card onto a target element (a column section
 * for status moves, another card for priority reorder) by dispatching the
 * drag events the board listens for, then wait for the board reload.
 */
export async function dragCardOnto(
  page: Page,
  num: number,
  target: ReturnType<Page["locator"]>
): Promise<void> {
  const source = card(page, num);
  const dataTransfer = await page.evaluateHandle(() => new DataTransfer());
  await source.dispatchEvent("dragstart", { dataTransfer });
  await target.dispatchEvent("dragover", { dataTransfer });
  await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes(`/api/projects/${PROJECT}/tasks`) && r.request().method() === "GET"
    ),
    target.dispatchEvent("drop", { dataTransfer }),
  ]);
}

/** A valid 1x1 PNG, small enough to inline, for screenshot upload tests. */
export const TINY_PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==",
  "base64"
);
