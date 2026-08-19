import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: ".",
  testMatch: "mapper-reconciliation.spec.ts",
  fullyParallel: false,
  workers: 1,
  timeout: 90_000,
  expect: { timeout: 10_000 },
  outputDir: "../tmp/playwright-results",
  reporter: [["list"]],
  use: {
    headless: true,
    viewport: { width: 1440, height: 1000 },
	// Reconciliation preview/apply tokens are intentionally ephemeral and must
	// not be retained in a shareable Playwright network trace.
	trace: "off",
    screenshot: "only-on-failure",
  },
});
