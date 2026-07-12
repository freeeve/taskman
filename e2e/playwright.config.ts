import { defineConfig, devices } from "@playwright/test";
import { BASE_URL } from "./helpers";

/**
 * Playwright config for the taskman web UI e2e suite.
 *
 * The suite attaches to an already-running `taskman serve` instance
 * (http://localhost:8311 by default; override with E2E_BASE_URL). Tests run
 * single-worker because every mutation writes through to the shared on-disk
 * store and priority order file.
 *
 * The store is a shared git repo that other sessions (and the web UI) commit to
 * concurrently, and taskman serializes every mutation behind a cross-process
 * lock. Under heavy multi-writer load a single mutation's response can queue for
 * many seconds, so the per-test timeout is generous (a real failure still fails,
 * just later) and one retry absorbs a lone contention flake without masking a
 * reproducible break.
 */
export default defineConfig({
  testDir: "./tests",
  globalSetup: "./global-setup",
  globalTeardown: "./global-teardown",
  fullyParallel: false,
  workers: 1,
  retries: 1,
  timeout: 45_000,
  expect: { timeout: 5_000 },
  use: {
    baseURL: BASE_URL,
    trace: "retain-on-failure",
    viewport: { width: 1280, height: 800 },
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
