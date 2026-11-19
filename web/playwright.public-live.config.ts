import { defineConfig } from '@playwright/test';
export default defineConfig({
    testDir: './e2e', testMatch: 'public-live.spec.ts', workers: 1,
    use: { headless: true, launchOptions: { args: ['--no-sandbox'] } },
});
