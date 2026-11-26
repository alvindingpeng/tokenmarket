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
        const scroller = viewport.width <= 767 || viewport.height <= 600
            ? page.locator('.responsive-page')
            : wallet;
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
            await page.waitForTimeout(20);
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
        await expect(page.locator(name === 'Home' ? '.home-summary-card' : '.settings-page').first()).toBeVisible();
        await expect.poll(() => scroller.evaluate(el => el.scrollHeight - el.clientHeight)).toBeGreaterThan(50);
        const box = (await scroller.boundingBox())!;
        const x = Math.round(box.x + box.width * 0.6);
        const from = Math.round(box.y + box.height * 0.8);
        const to = Math.round(box.y + box.height * 0.25);
        await session.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y: from }] });
        for (let step = 1; step <= 5; step++) {
            await session.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x, y: Math.round(from + (to - from) * step / 5) }] });
            await page.waitForTimeout(20);
        }
        await session.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
        await expect.poll(() => scroller.evaluate(el => Math.max(el.scrollTop, ...Array.from(el.querySelectorAll('*'), child => child.scrollTop)))).toBeGreaterThan(0);
    }
    await context.close();
});

for (const viewport of [{ width: 390, height: 844 }, { width: 667, height: 375 }, { width: 844, height: 390 }, { width: 1440, height: 900 }]) {
    for (const pageName of ['notification', 'ops']) {
        if (viewport.width === 1440 && pageName !== 'notification') continue;
        test(`${pageName} content is reachable by touch at ${viewport.width}x${viewport.height}`, async ({ browser }) => {
            const context = await browser.newContext({ viewport, isMobile: viewport.width < 1440, hasTouch: true, reducedMotion: 'reduce' });
            const page = await context.newPage();
            await page.addInitScript((currentPage) => {
                localStorage.setItem('nav-storage', JSON.stringify({ state: { currentPage, settingSection: 'personal' }, version: 0 }));
            }, pageName);
            const notifications = Array.from({ length: 20 }, (_, index) => ({
                id: index + 1, user_id: 1, type: 'system', status: 'read',
                title: index === 0 ? 'notification-id-'.repeat(24) : `Notification ${index + 1}`,
                content: 'A notification with enough content to occupy space.',
                created_at: '2026-11-25T00:00:00Z',
            }));
            await page.route('**/api/v1/**', async route => {
                const path = new URL(route.request().url()).pathname;
                const data = path === '/api/v1/user/status'
                    ? { id: 1, username: 'demo', role: 'admin', status: 'active', balance: 100 }
                    : path === '/api/v1/ops/overview'
                        ? {
                            requests_24h: 10, success_24h: 9, failed_24h: 1,
                            input_tokens_24h: 120, output_tokens_24h: 30, cost_24h: 1,
                            active_requests: 0, channels_total: 2, channels_enabled: 2, alerts_24h: 0,
                        }
                    : path === '/api/v1/ops/reconcile/list'
                        ? {
                            items: Array.from({ length: 12 }, (_, index) => ({
                                day: `2026-11-${String(14 - index).padStart(2, '0')}`,
                                records: 10, user_cost: 1, owner_revenue: 1,
                                platform_revenue: 0, duplicated: 0, missing: 0,
                                stale_hold: 0, unsettled: 0, status: 'ok',
                                note: '', created_at: '2026-11-25T00:00:00Z',
                            })),
                        }
                    : path === '/api/v1/notification/list'
                        ? { items: notifications, total: 40, page: 1, page_size: 20 }
                        : path === '/api/v1/notification/unread-count' ? { unread_count: 0 } : [];
                await route.fulfill({ json: { data } });
            });
            await page.goto('/');
            await page.locator('#initial-loader').waitFor({ state: 'hidden' });
            await page.getByRole('navigation', { name: 'Main Navigation' }).getByRole('button', { name: pageName === 'notification' ? 'Notification' : 'Ops', exact: true }).click();
            const content = page.locator(pageName === 'notification' ? '.notification-page' : '.ops-page');
            await expect(content).toBeVisible();
            if (pageName === 'notification') {
                await expect(page.getByText('Notification 20')).toHaveCount(1);
                await expect.poll(() => content.evaluate(el => Math.abs(el.getBoundingClientRect().width - el.parentElement!.clientWidth))).toBeLessThanOrEqual(1);
                expect(await content.evaluate(el => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(1);
                const regularCard = content.locator('[data-slot=card]').filter({ hasText: /^Notification 20/ });
                expect(await regularCard.evaluate(el => el.clientHeight)).toBeLessThan(100);
            }

            const scroller = viewport.width <= 767 || viewport.height <= 600
                ? page.locator('.responsive-page')
                : content;
            await expect.poll(() => scroller.evaluate(el => el.scrollHeight - el.clientHeight)).toBeGreaterThan(40);
            const box = (await scroller.boundingBox())!;
            const x = Math.round(box.x + box.width * 0.55);
            const from = Math.round(box.y + box.height * 0.75);
            const to = Math.round(box.y + box.height * 0.25);
            const session = await context.newCDPSession(page);
            await session.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y: from }] });
            for (let step = 1; step <= 5; step++) {
                await session.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x, y: Math.round(from + (to - from) * step / 5) }] });
            await page.waitForTimeout(20);
            }
            await session.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
            await expect.poll(() => scroller.evaluate(el => Math.max(el.scrollTop, ...Array.from(el.querySelectorAll('*'), child => child.scrollTop)))).toBeGreaterThan(0);
            if (pageName === 'notification') {
                await page.getByText('Notification 20').scrollIntoViewIfNeeded();
                await expect(page.getByText('Notification 20')).toBeInViewport();
            } else {
                const lastTab = content.getByRole('tab').last();
                await lastTab.scrollIntoViewIfNeeded();
                await expect(lastTab).toBeInViewport();
                await lastTab.click();
            }
            await context.close();
        });
    }
}

for (const viewport of [{ width: 390, height: 844 }, { width: 667, height: 375 }]) {
    for (const target of [
        { page: 'webhook', section: 'personal', label: 'webhook', expectedText: 'https://hook.example.com/20' },
        { page: 'tickets', section: 'personal', label: 'tickets', expectedText: 'Ticket 20' },
        { page: 'apikey', section: 'personal', label: 'apikey', expectedText: 'Key 20' },
        { page: 'setting', section: 'audit', label: 'setting-audit', expectedText: 'channel.update' },
    ] as const) {
        test(`${target.label} table/list scrolls with touch at ${viewport.width}x${viewport.height}`, async ({ browser }) => {
            const context = await browser.newContext({ viewport, isMobile: true, hasTouch: true, reducedMotion: 'reduce' });
            const page = await context.newPage();
            const consoleErrors: string[] = [];
            page.on('console', msg => { if (msg.type() === 'error') consoleErrors.push(msg.text()); });
            await page.addInitScript(({ currentPage, settingSection }) => {
                localStorage.setItem('theme', 'compact');
                localStorage.setItem('nav-storage', JSON.stringify({ state: { currentPage, settingSection }, version: 0 }));
            }, { currentPage: target.page, settingSection: target.section });

            const now = '2026-11-25T10:00:00Z';
            const items20 = <T,>(fn: (index: number) => T) => Array.from({ length: 20 }, (_, i) => fn(i + 1));
            await page.route('**/api/v1/**', async route => {
                const path = new URL(route.request().url()).pathname;
                const data = path === '/api/v1/user/status'
                    ? { id: 1, username: 'admin', role: 'admin', status: 'active', balance: 100 }
                    : path === '/api/v1/apikey/list'
                        ? items20(i => ({ id: i, name: `Key ${i}`, key: `sk-test-${i}`, status: 1, enabled: true, quota: 100, used: 10, created_at: now, updated_at: now, groups: ['auto'] }))
                    : path.startsWith('/api/v1/webhook/list')
                        ? { items: items20(i => ({ id: i, url: `https://hook.example.com/${i}`, events: ['billing.low_balance'], enabled: true, retry_count: 3, created_at: now, updated_at: now })), total: 20 }
                    : path.includes('/ticket/list')
                        ? { items: items20(i => ({ id: i, title: `Ticket ${i}`, category: 'billing', priority: 'normal', status: 'open', user_id: 1, username: 'admin', created_at: now, updated_at: now })), total: 20 }
                    : path.startsWith('/api/v1/audit/list')
                        ? { items: items20(i => ({ id: i, actor_id: 1, actor_name: 'admin', action: 'channel.update', target_type: 'channel', target_id: String(i), ip: '127.0.0.1', created_at: now })), total: 20 }
                    : [];
                await route.fulfill({ json: { data } });
            });

            await page.goto('/');
            await page.locator('#initial-loader').waitFor({ state: 'hidden' });
            const scroller = page.locator('.responsive-page').first();
            await expect(page.getByText(target.expectedText).first()).toBeVisible();
            await expect.poll(() => scroller.evaluate(el => el.scrollHeight - el.clientHeight)).toBeGreaterThan(100);

            const box = (await scroller.boundingBox())!;
            const x = Math.round(box.x + box.width * 0.5);
            const from = Math.round(box.y + box.height * 0.78);
            const to = Math.round(box.y + box.height * 0.22);
            const session = await context.newCDPSession(page);
            await session.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y: from }] });
            for (let step = 1; step <= 5; step++) {
                await session.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x, y: Math.round(from + (to - from) * step / 5) }] });
                await page.waitForTimeout(20);
            }
            await session.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
            await expect.poll(() => scroller.evaluate(el => el.scrollTop)).toBeGreaterThan(0);
            expect(consoleErrors).toEqual([]);
            await context.close();
        });
    }
}

for (const width of [390, 1440]) {
    test(`compact price cards keep model details and actions visible at ${width}px`, async ({ page }) => {
        await page.setViewportSize({ width, height: 844 });
        await page.route('**/api/v1/**', async route => {
            const path = new URL(route.request().url()).pathname;
            const data = path === '/api/v1/user/status'
                ? { id: 1, username: 'demo', role: 'admin', status: 'active', balance: 100 }
                : path === '/api/v1/model/list'
                    ? [{
                        name: 'example-model', input: 1.25, output: 4.5, cache_read: 0.25, cache_write: 0.75,
                        reference: { context_length: 128000, max_output: 16000, price_provider: 'example', official_price: { input: 2, output: 5 } },
                    }]
                    : [];
            await route.fulfill({ json: { data } });
        });
        await page.goto('/');
        await page.locator('#initial-loader').waitFor({ state: 'hidden' });
        await page.getByRole('button', { name: 'EN', exact: true }).click();
        await page.getByRole('navigation', { name: 'Main Navigation' }).getByRole('button', { name: 'Model', exact: true }).click();
        const card = page.locator('article').filter({ hasText: 'example-model' });
        await expect(card.getByText('1.25/0.25$')).toBeVisible();
        await expect(card.getByText('4.50/0.75$')).toBeVisible();
        await expect(card.getByText('Context 128,000')).toBeVisible();
        await expect(card.getByText('Output 16,000')).toBeVisible();
        await expect(card.getByText(/Developer reference/)).toBeVisible();
        expect(await card.evaluate(el => el.clientHeight)).toBeLessThan(160);
        expect(await card.evaluate(el => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(1);
        await card.getByRole('button', { name: 'Task capability and verification' }).click();
        await expect(card.getByRole('button', { name: 'Save task capability' })).toBeVisible();
        expect(await card.evaluate(el => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(1);
    });
}
