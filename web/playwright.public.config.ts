import { defineConfig } from '@playwright/test';

// Browser regression tests load the embedded HTML directly from this checkout.
// They need neither a running gateway nor login credentials nor a Vite server.
export default defineConfig({
    testDir: './e2e',
    testMatch: 'public-status.spec.ts',
    forbidOnly: !!process.env.CI,
    workers: 1,
    use: { baseURL: 'http://status.test', headless: true,
        launchOptions: { args: ['--no-sandbox'] } },
});
