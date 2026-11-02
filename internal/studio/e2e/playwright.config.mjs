import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  timeout: 30_000,
  use: { baseURL: 'http://127.0.0.1:4321', headless: true },
  webServer: {
    // fake-bin 里的假 claude 只对这个 Studio 进程生效，见 fake-bin/claude。
    command: 'cd ../../.. && PATH="$PWD/internal/studio/e2e/fake-bin:$PATH" ORCHESTRATOR=claude RENDERER=claude go run ./cmd/am studio --listen 127.0.0.1:4321 --config "$AM_STUDIO_E2E_CONFIG"',
    url: 'http://127.0.0.1:4321/api/health',
    reuseExistingServer: false,
    timeout: 120_000,
  },
});
