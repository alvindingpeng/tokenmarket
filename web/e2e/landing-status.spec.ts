import { test, expect } from '@playwright/test';

// Source-rendered landing page should expose the public route on mobile too.
for (const width of [360, 1280]) {
    test('guest landing exposes public model status at ' + width, async ({ page }) => {
        await page.setViewportSize({ width, height: 844 });
        await page.goto('/');
        await expect(page.locator('a[href="/status"]:visible').first()).toBeVisible();
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
    });
}
