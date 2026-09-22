import { defineConfig, devices } from "@playwright/test";

// Driven by e2e/run.sh (make e2e-web), which builds the binary, starts the
// servers and exports their URLs. Running `npx playwright test` directly
// without them fails fast in e2e/support.ts rather than testing nothing.
//
// No retries: a flaky flow is fixed at its cause or deleted, never retried
// into greenness (D228).
export default defineConfig({
  testDir: "./e2e",
  testMatch: "*.spec.ts",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  forbidOnly: !!process.env.CI,
  reporter: process.env.CI ? [["list"], ["github"]] : "list",
  timeout: 30_000,
  expect: { timeout: 10_000 },
  use: {
    ...devices["Desktop Chrome"],
    viewport: { width: 1440, height: 900 },
    trace: "retain-on-failure",
  },
});
