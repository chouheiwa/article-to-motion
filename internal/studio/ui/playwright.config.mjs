import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  workers: 1,
  use: { baseURL: 'http://127.0.0.1:4321', headless: true },
  webServer: {
    // e2e/fake-bin 里的假 claude 只对这个 Studio 进程生效，见 e2e/fake-bin/claude。
    // Studio 二进制内嵌的是 ../web 下已提交的构建产物，改了 src 要先 npm run build。
    command:
      'cd ../../.. && PATH="$PWD/internal/studio/ui/e2e/fake-bin:$PATH" ORCHESTRATOR=claude RENDERER=claude go run ./cmd/am studio --listen 127.0.0.1:4321 --config "$AM_STUDIO_E2E_CONFIG"',
    url: 'http://127.0.0.1:4321/api/health',
    reuseExistingServer: false,
    timeout: 120_000,
  },
});
