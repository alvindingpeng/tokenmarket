import { expect, test } from '@playwright/test';

const modelName = 'provider/extra-long-model-identifier-'.repeat(5);
const channelName = 'channel-region-and-environment-'.repeat(5);
const keyName = 'credential-with-a-long-display-name-'.repeat(4);
const group = {
    id: 1,
    name: 'Routing layout test',
    mode: 'score',
    relay_config: { auto_routing: 'active', auto_tool: '7', canary_item_id: 9, canary_percent: 10 },
    runtime: { current_item_id: 9, cooldowns: {} },
    items: [{
        id: 9, channel_grant_id: 7, channel_id: 3, model_name: modelName,
        channel_name: channelName, key_name: keyName, protocols: 10, available: true,
        context_length: 200000, max_output: 16384,
    }],
};

for (const viewport of [
    { width: 320, height: 640 },
    { width: 667, height: 320 },
    { width: 768, height: 1024 },
    { width: 1440, height: 900 },
]) {
    test(`routing settings fit and scroll at ${viewport.width}×${viewport.height}`, async ({ browser }) => {
        const context = await browser.newContext({
            viewport, hasTouch: viewport.width < 1000, isMobile: viewport.width < 768, reducedMotion: 'reduce',
        });
        const page = await context.newPage();
        const english = viewport.width !== 320;
        const routing = english ? 'Routing' : '路由策略';
        await page.addInitScript((locale) => {
            localStorage.setItem('switchyard-settings', JSON.stringify({ state: { locale }, version: 0 }));
            localStorage.setItem('theme', 'compact');
            localStorage.setItem('nav-storage', JSON.stringify({ state: { currentPage: 'group' }, version: 0 }));
        }, english ? 'en' : 'zh_hans');
        await page.route('**/api/v1/**', async route => {
            const path = new URL(route.request().url()).pathname;
            if (path === '/api/v1/group/events') {
                await route.fulfill({ contentType: 'text/event-stream', body: ': ready\n\n' });
                return;
            }
            const data = path === '/api/v1/user/status'
                ? { id: 1, username: 'demo', role: 'admin', status: 'active', balance: 100 }
                : path === '/api/v1/group/list' ? [group]
                : path === '/api/v1/group/metrics/1' ? { items: [] }
                : path === '/api/v1/model/task-capability/probe-tool'
                    ? { success: true, evaluation: [{ task: 'tool', passed: 3, total: 3, duration_ms: 12345 }] }
                    : [];
            await route.fulfill({ json: { data } });
        });
        await page.goto('/');
        await page.locator('#initial-loader').waitFor({ state: 'hidden' });
        await page.locator('article').filter({ hasText: group.name }).getByRole('button', { name: /^Open dialog/ }).click();
        const dialog = page.getByRole('dialog');
        await dialog.getByRole('tab', { name: routing }).click();
        const panel = dialog.getByRole('tabpanel', { name: routing });
        const scroller = dialog.locator('form > div').first();
        await expect.poll(() => scroller.evaluate(el => el.scrollHeight - el.clientHeight)).toBeGreaterThan(100);
        if (viewport.width < 1000) {
            const box = (await scroller.boundingBox())!;
            const x = Math.round(box.x + box.width / 2);
            const start = box.y + box.height * 0.8;
            const end = box.y + box.height * 0.3;
            const session = await context.newCDPSession(page);
            await session.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y: start }] });
            for (let step = 1; step <= 5; step++) {
                await session.send('Input.dispatchTouchEvent', {
                    type: 'touchMove', touchPoints: [{ x, y: start + (end - start) * step / 5 }],
                });
            }
            await session.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
            await expect.poll(() => scroller.evaluate(el => el.scrollTop)).toBeGreaterThan(10);
        }

        const candidate = panel.locator('label[for=auto_tool-7]');
        await expect(candidate).toContainText(modelName);
        await expect(candidate).toContainText(channelName);
        await expect(candidate).toContainText(keyName);
        await expect(candidate).toContainText('200000');
        const lines = await candidate.getByText(modelName, { exact: true }).evaluate(el => {
            const range = document.createRange();
            range.selectNodeContents(el);
            const bounds = el.getBoundingClientRect();
            const rects = [...range.getClientRects()];
            return { count: rects.length, contained: rects.every(rect => rect.right <= bounds.right + 1) };
        });
        expect(lines.count).toBeGreaterThan(1);
        expect(lines.contained).toBe(true);

        const probe = panel.getByRole('button', { name: english ? 'Evaluate Anthropic streaming tools and coding' : '评测 Anthropic 流式工具与编程' });
        await probe.click();
        await expect(panel.getByText(/(Tools|工具) 3\/3.*12345 (ms|毫秒)/)).toBeVisible();
        await expect(panel.getByRole('checkbox', { name: english ? 'Verified for Anthropic streaming tools' : '已验证 Anthropic 流式工具' })).toBeChecked();
        await expect(candidate.getByRole('checkbox')).toBeChecked();

        const canary = panel.locator('#group-canary-member');
        await canary.click();
        const option = page.getByRole('option').filter({ hasText: modelName });
        await expect(option).toBeInViewport();
        expect(await option.evaluate(el => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(1);
        await option.click();
        await expect(canary).toContainText(modelName);
        expect(await canary.evaluate(el => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(1);
        expect(await scroller.evaluate(el => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(1);
        await panel.locator('#group-canary-per-request').scrollIntoViewIfNeeded();
        await expect(panel.locator('#group-canary-per-request')).toBeInViewport();
        await expect(dialog.getByRole('button', { name: english ? 'Save' : '保存', exact: true })).toBeInViewport();
        await context.close();
    });
}
