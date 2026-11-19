import { test, expect } from '@playwright/test';

// Optional release smoke against the actual deployed service, no credentials.
// Run with PUBLIC_STATUS_BASE_URL=https://your-gateway pnpm exec playwright test --config playwright.public-live.config.ts
const base = process.env.PUBLIC_STATUS_BASE_URL;
test.skip(!base, 'Set PUBLIC_STATUS_BASE_URL to verify a running public endpoint');
test('live anonymous public status has safe contract and renders', async ({ page, request }) => {
    const response = await request.get(base + '/api/v1/public/channel/stats');
    expect(response.ok()).toBe(true);
    const payload = (await response.json()).data;
    expect(payload.window_seconds).toBe(900);
    expect(payload.refresh_seconds).toBe(15);
    expect(Array.isArray(payload.models)).toBe(true);
    for (const row of payload.models) {
        expect(row.channel_code).toMatch(/^\d+$/);
        expect(row.history).toHaveLength(15);
        for (const key of ['channel_name','channel_id','user_id','base_url','key','client_ip','supply_price','error']) expect(row).not.toHaveProperty(key);
    }
    await page.goto(base + '/status');
    await expect(page.locator('h1')).toBeVisible();
    if (payload.models.length) await expect(page.locator('[data-testid="model-card"]')).not.toHaveCount(0);
    await expect(page.getByRole('alert')).toBeHidden();
});
