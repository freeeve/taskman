import { defineConfig, devices } from "@playwright/test";
import { BASE_URL } from "./helpers";

/**
 * Playwright config for the taskman web UI e2e suite.
 *
 * The suite attaches to an already-running `taskman serve` instance
 * (http://localhost:8311 by default; override with E2E_BASE_URL). Tests run
 * single-worker because every mutation writes through to the shared on-disk
 * store and priority order file.
 */
export default defineConfig({
  testDir: "./tests",
  globalSetup: "./global-setup",
  globalTeardown: "./global-teardown",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 20_000,
  expect: { timeout: 5_000 },
  use: {
    baseURL: BASE_URL,
    trace: "retain-on-failure",
    viewport: { width: 1280, height: 800 },
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
