import { test, expect } from "@playwright/test";
import { BASE_URL, PROJECT, SEEDS, getTasks, taskByTitle } from "../helpers";

/**
 * Contract tests for the JSON API, driven at the HTTP layer. Everything in
 * this file is read-only or a rejected (non-mutating) request.
 */

const base = `${BASE_URL}/api/projects/${PROJECT}`;

test.describe("GET /api/projects", () => {
  test("lists the sandbox project with open and deferred counts", async ({ request }) => {
    const res = await request.get(`${BASE_URL}/api/projects`);
    expect(res.ok()).toBeTruthy();
    const projects = await res.json();
    const sandbox = projects.find((p: { name: string }) => p.name === PROJECT);
    expect(sandbox).toBeTruthy();
    expect(typeof sandbox.open).toBe("number");
    expect(typeof sandbox.deferred).toBe("number");
    expect(sandbox.open).toBeGreaterThanOrEqual(3);
    expect(sandbox.deferred).toBeGreaterThanOrEqual(1);
  });
});

test.describe("GET /api/projects/{p}/tasks", () => {
  test("returns tasks, order, and lanes with the full wire shape", async ({ request }) => {
    const { tasks, order, lanes } = await getTasks(request);
    expect(tasks.length).toBeGreaterThanOrEqual(6);
    for (const t of tasks) {
      expect(t.num).toBeGreaterThan(0);
      expect(t.slug).toMatch(/^[a-z0-9][a-z0-9-]*$/);
      expect(["pending", "in-progress", "done"]).toContain(t.status);
      expect(typeof t.deferred).toBe("boolean");
      expect(t.file).toMatch(/\.md$/);
      expect(t.title.length).toBeGreaterThan(0);
    }
    expect(Array.isArray(order)).toBeTruthy();
    expect(lanes).toEqual([...lanes].sort());
    expect(lanes).toContain("web");
    expect(lanes).toContain("e2e");
  });

  test("carries the seeded statuses and deferral flag", async ({ request }) => {
    const { tasks } = await getTasks(request);
    const byTitle = new Map(tasks.map((t) => [t.title, t]));
    expect(byTitle.get(SEEDS.pendingWeb)?.status).toBe("pending");
    expect(byTitle.get(SEEDS.pendingWeb)?.lane).toBe("web");
    expect(byTitle.get(SEEDS.inProgress)?.status).toBe("in-progress");
    expect(byTitle.get(SEEDS.done)?.status).toBe("done");
    expect(byTitle.get(SEEDS.deferred)?.deferred).toBe(true);
  });
});

test.describe("GET /api/projects/{p}/tasks/{n}", () => {
  test("returns the task, raw markdown body, and rendered html", async ({ request }) => {
    const seed = await taskByTitle(request, SEEDS.pendingWeb);
    const res = await request.get(`${base}/tasks/${seed.num}`);
    expect(res.ok()).toBeTruthy();
    const detail = await res.json();
    expect(detail.task.num).toBe(seed.num);
    expect(detail.body).toMatch(/^# /);
    expect(detail.html).toContain("<h1");
    expect(detail.html).toContain(SEEDS.pendingWeb);
  });

  test("resolves a slug fragment like the CLI does", async ({ request }) => {
    const seed = await taskByTitle(request, SEEDS.pendingWeb);
    const res = await request.get(`${base}/tasks/pending-alpha`);
    expect(res.ok()).toBeTruthy();
    const detail = await res.json();
    expect(detail.task.num).toBe(seed.num);
  });
});

test.describe("error handling", () => {
  test("404s an unknown project with the uniform error shape", async ({ request }) => {
    const res = await request.get(`${BASE_URL}/api/projects/no-such-project-xyz/tasks`);
    expect(res.status()).toBe(404);
    expect((await res.json()).error).toBeTruthy();
  });

  test("404s a project name that fails the slug guard", async ({ request }) => {
    const res = await request.get(`${BASE_URL}/api/projects/Not_A_Slug/tasks`);
    expect(res.status()).toBe(404);
  });

  test("404s an unknown task number", async ({ request }) => {
    const res = await request.get(`${base}/tasks/999999`);
    expect(res.status()).toBe(404);
  });

  test("400s an invalid status value without mutating", async ({ request }) => {
    const seed = await taskByTitle(request, SEEDS.pendingWeb);
    const res = await request.post(`${base}/tasks/${seed.num}/status`, {
      data: { status: "bogus" },
    });
    expect(res.status()).toBe(400);
    expect((await taskByTitle(request, SEEDS.pendingWeb)).status).toBe("pending");
  });

  test("400s a defer without a reason", async ({ request }) => {
    const seed = await taskByTitle(request, SEEDS.pendingWeb);
    const res = await request.post(`${base}/tasks/${seed.num}/defer`, {
      data: { reason: "  " },
    });
    expect(res.status()).toBe(400);
    expect((await taskByTitle(request, SEEDS.pendingWeb)).deferred).toBe(false);
  });

  test("rejects an over-long description cleanly without leaking the store path", async ({
    request,
  }) => {
    const tooLong = "a".repeat(300);
    for (const endpoint of ["features", "tasks"]) {
      const res = await request.post(`${base}/${endpoint}`, { data: { description: tooLong } });
      expect(res.status()).toBe(400);
      const err = (await res.json()).error as string;
      expect(err).toContain("description too long");
      // The absolute store path must never reach the client.
      expect(err).not.toMatch(/\/Users\/|\.taskman\//);
    }
  });

  test("still rejects an empty-slug description with a clean message", async ({ request }) => {
    const res = await request.post(`${base}/features`, { data: { description: "!!!" } });
    expect(res.status()).toBe(400);
    expect((await res.json()).error).toContain("empty slug");
  });

  test("rejects malformed and path-traversal project names at the boundary", async ({ request }) => {
    // The project path segment is the traversal guard: only [a-z0-9][a-z0-9-]*
    // resolves to a store directory, so uppercase, dots, underscores, a leading
    // dash, and encoded traversal all fail before touching the filesystem.
    for (const bad of ["E2E-SANDBOX", "bad.name", "under_score", "-leading", "..%2f..%2fetc"]) {
      const res = await request.get(`${BASE_URL}/api/projects/${bad}/tasks`);
      expect(res.status(), `${bad} must be rejected`).toBe(404);
    }
    // A well-formed project still resolves (the guard is not over-broad).
    expect((await request.get(`${base}/tasks`)).status()).toBe(200);
  });
});

test.describe("GET /api/projects/{p}/features", () => {
  test("returns a feature list", async ({ request }) => {
    const res = await request.get(`${base}/features`);
    expect(res.ok()).toBeTruthy();
    expect(Array.isArray(await res.json())).toBeTruthy();
  });
});
