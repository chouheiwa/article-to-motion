import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  timeout: 30_000,
  use: { baseURL: 'http://127.0.0.1:4321', headless: true },
  webServer: {
    command: 'cd ../../.. && go run ./cmd/am studio --listen 127.0.0.1:4321 --config "$AM_STUDIO_E2E_CONFIG"',
    url: 'http://127.0.0.1:4321/api/health',
    reuseExistingServer: false,
    timeout: 120_000,
  },
});
