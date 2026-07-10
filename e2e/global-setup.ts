import { request, type APIRequestContext } from "@playwright/test";
import { BASE_URL, PROJECT, SEEDS, type TaskJSON } from "./helpers";

/**
 * Global setup: verify the server and sandbox project are reachable, then
 * seed the baseline fixture tasks the read-only specs assert against.
 * Seeding is idempotent -- existing seeds (matched by title) are reconciled
 * to their expected status rather than recreated.
 */
export default async function globalSetup(): Promise<void> {
  const ctx = await request.newContext();
  try {
    await verify(ctx);
    await seed(ctx);
  } finally {
    await ctx.dispose();
  }
}

/** Fail fast with actionable messages when the environment is not ready. */
async function verify(ctx: APIRequestContext): Promise<void> {
  let projects: { name: string }[];
  try {
    const res = await ctx.get(`${BASE_URL}/api/projects`);
    if (!res.ok()) throw new Error(`GET /api/projects returned ${res.status()}`);
    projects = await res.json();
  } catch (err) {
    throw new Error(
      `taskman web UI is not reachable at ${BASE_URL} (${err}). ` +
        `Start it with \`taskman serve -addr 127.0.0.1:8311\` or point the suite ` +
        `elsewhere with E2E_BASE_URL.`
    );
  }
  if (!projects.some((p) => p.name === PROJECT)) {
    throw new Error(
      `sandbox project ${JSON.stringify(PROJECT)} does not exist in the store. ` +
        `The suite mutates its project (tasks, order, screenshots, one commit per ` +
        `mutation), so it refuses to run against a real ledger. Create the sandbox ` +
        `with: mkdir -p ~/.taskman/${PROJECT}/tasks (or set E2E_PROJECT).`
    );
  }
}

/** Desired baseline: title -> {lane, status, deferred}. */
const BASELINE: {
  title: string;
  lane: string;
  status: "pending" | "in-progress" | "done";
  deferred: boolean;
}[] = [
  { title: SEEDS.pendingWeb, lane: "web", status: "pending", deferred: false },
  { title: SEEDS.pendingE2E, lane: "e2e", status: "pending", deferred: false },
  { title: SEEDS.pendingBare, lane: "", status: "pending", deferred: false },
  { title: SEEDS.inProgress, lane: "", status: "in-progress", deferred: false },
  { title: SEEDS.done, lane: "", status: "done", deferred: false },
  { title: SEEDS.deferred, lane: "", status: "pending", deferred: true },
];

/** Create missing seeds and reconcile drifted ones back to the baseline. */
async function seed(ctx: APIRequestContext): Promise<void> {
  const base = `${BASE_URL}/api/projects/${PROJECT}`;
  const res = await ctx.get(`${base}/tasks`);
  const { tasks } = (await res.json()) as { tasks: TaskJSON[] };
  const byTitle = new Map(tasks.map((t) => [t.title, t]));

  for (const want of BASELINE) {
    let t = byTitle.get(want.title);
    if (!t) {
      const created = await ctx.post(`${base}/tasks`, {
        data: { description: want.title, lane: want.lane },
      });
      if (created.status() !== 201) {
        throw new Error(
          `seeding ${JSON.stringify(want.title)} failed: ${await created.text()}`
        );
      }
      t = (await created.json()) as TaskJSON;
    }
    if (t.deferred && !want.deferred) {
      await ctx.post(`${base}/tasks/${t.num}/resume`);
      t = { ...t, deferred: false };
    }
    if (t.status !== want.status) {
      await ctx.post(`${base}/tasks/${t.num}/status`, { data: { status: want.status } });
    }
    if (want.deferred && !t.deferred) {
      await ctx.post(`${base}/tasks/${t.num}/defer`, { data: { reason: "e2e fixture" } });
    }
  }
}
