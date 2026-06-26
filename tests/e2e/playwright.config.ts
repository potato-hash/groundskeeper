import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: '.',
  testMatch: '*.spec.ts',
  timeout: 30000,
  retries: process.env.CI ? 2 : 0,
  use: {
    baseURL: 'http://localhost:19999',
    headless: true,
    viewport: { width: 1280, height: 800 },
  },
  projects: [
    { name: 'chromium', use: { browserName: 'chromium' } },
  ],
  webServer: {
    command: 'AGENTDECK_PROFILE=_test go run ../../cmd/groundskeeper web --no-tui --listen 127.0.0.1:19999 --token test',
    url: 'http://localhost:19999/healthz',
    reuseExistingServer: !process.env.CI,
    timeout: 30000,
  },
})
