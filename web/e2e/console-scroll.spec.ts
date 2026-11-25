import { expect, test } from '@playwright/test';

for (const viewport of [{ width: 390, height: 844 }, { width: 844, height: 390 }, { width: 667, height: 375 }]) {
    test(`wallet content scrolls with touch at ${viewport.width}x${viewport.height}`, async ({ browser }) => {
        const context = await browser.newContext({ viewport, isMobile: true, hasTouch: true, reducedMotion: 'reduce' });
        const page = await context.newPage();
        await page.addInitScript(() => {
            localStorage.setItem('theme', 'compact');
            localStorage.setItem('nav-storage', JSON.stringify({ state: { currentPage: 'wallet', settingSection: 'personal' }, version: 0 }));
        });
        await page.route('**/api/v1/**', async route => {
            const path = new URL(route.request().url()).pathname;
            const data = path === '/api/v1/user/status'
                ? { id: 1, username: 'demo', role: 'admin', status: 'active', balance: 100 }
                : path === '/api/v1/user/balance-alert/history' ? { items: [] }
                : path === '/api/v1/wallet/summary'
                    ? { balance: 100, frozen: 0, total_spent: 5, total_revenue: 0, total_recharged: 105 }
                    : path.includes('/stats/balance/ledger') ? { items: [], total: 0 }
                    : [];
            await route.fulfill({ json: { data } });
        });
        await page.goto('/');
        const wallet = page.locator('.wallet-page');
        await expect(wallet).toBeVisible();
        await page.locator('#initial-loader').waitFor({ state: 'hidden' });
        const scroller = viewport.height <= 600 ? page.locator('.responsive-page') : wallet;
        const box = (await scroller.boundingBox())!;
        expect(box.height).toBeGreaterThan(40);
        const before = await scroller.evaluate(el => ({ top: el.scrollTop, max: el.scrollHeight - el.clientHeight }));
        expect(before.max).toBeGreaterThan(100);

        const session = await context.newCDPSession(page);
        const x = Math.round(box.x + box.width / 2);
        const from = Math.round(box.y + box.height * 0.8);
        const to = Math.round(box.y + box.height * 0.25);
        await session.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y: from }] });
        for (let step = 1; step <= 5; step++) {
            await session.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x, y: Math.round(from + (to - from) * step / 5) }] });
        }
        await session.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
        await expect.poll(() => scroller.evaluate(el => el.scrollTop)).toBeGreaterThan(before.top);
        const tab = wallet.getByRole('tab').first();
        await tab.scrollIntoViewIfNeeded();
        await expect(tab).toBeInViewport();
        await tab.click();
        await context.close();
    });
}

test('short landscape settings and home can scroll by touch', async ({ browser }) => {
    const context = await browser.newContext({ viewport: { width: 667, height: 375 }, isMobile: true, hasTouch: true, reducedMotion: 'reduce' });
    const page = await context.newPage();
    await page.route('**/api/v1/**', async route => {
        const path = new URL(route.request().url()).pathname;
        const metrics = { id: 1, input_token: 10, output_token: 5, input_cost: 1, output_cost: 1, wait_time: 1, request_success: 3, request_failed: 0 };
        const data = path === '/api/v1/user/status' ? { id: 1, username: 'demo', role: 'admin', status: 'active', balance: 100 }
            : path === '/api/v1/stats/home' ? { total: metrics, daily: { max_request_count: 0, items: [] }, hourly: [], channels: [] } : [];
        await route.fulfill({ json: { data } });
    });
    await page.goto('/');
    const nav = page.getByRole('navigation', { name: 'Main Navigation' });
    await nav.waitFor();
    await page.locator('#initial-loader').waitFor({ state: 'hidden' });
    const session = await context.newCDPSession(page);
    for (const name of ['Home', 'Setting']) {
        await nav.getByRole('button', { name, exact: true }).click();
        const scroller = page.locator('.responsive-page').last();
        await expect.poll(() => scroller.evaluate(el => el.scrollHeight - el.clientHeight)).toBeGreaterThan(50);
        const box = (await scroller.boundingBox())!;
        const x = Math.round(box.x + box.width * 0.6);
        const from = Math.round(box.y + box.height * 0.8);
        const to = Math.round(box.y + box.height * 0.25);
        await session.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y: from }] });
        for (let step = 1; step <= 5; step++) {
            await session.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x, y: Math.round(from + (to - from) * step / 5) }] });
        }
        await session.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
        await expect.poll(() => scroller.evaluate(el => Math.max(el.scrollTop, ...Array.from(el.querySelectorAll('*'), child => child.scrollTop)))).toBeGreaterThan(0);
    }
    await context.close();
});
