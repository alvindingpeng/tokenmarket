import { test, expect } from '@playwright/test';
import { readFileSync } from 'node:fs';

// Serve the working-tree embedded template, not a previously deployed binary.
// No login, production DB, or paid upstream request is needed for these cases.
function snapshot() {
    const now = new Date().toISOString();
    const price = { input: 1.2, output: 6, cache_read: 0.12, cache_write: 1.5 };
    return { generated_at: now, window_seconds: 900, refresh_seconds: 15, models: [
        { channel_code: '1001', model_name: 'alpha-model', kind: '', user_price: price,
          official_price: { input: 2, output: 10, cache_read: 0.2, cache_write: 2.5 },
          price_ratio: { input: 0.6, output: 0.6, cache_read: 0.6, cache_write: 0.6 },
          status: 'operational', attempts: 20, success_rate: 1, latency_ms: 250,
          cache_read_ratio: 0.5, history: Array.from({length: 15}, (_, i) => ({
              start: new Date(Date.now() - (15-i)*60000).toISOString(), status: i < 4 ? 'degraded' : 'operational', attempts: 2, success_rate: i < 4 ? 0.5 : 1,
          })) },
        { channel_code: '1002', model_name: '<img src=x onerror=alert(1)>', kind: '', user_price: price,
          official_price: null, price_ratio: { input: null, output: null, cache_read: null, cache_write: null },
          status: 'no_data', attempts: 0, success_rate: null, latency_ms: null, cache_read_ratio: null,
          history: Array.from({length: 15}, () => ({ start: now, status: 'no_data', attempts: 0, success_rate: null })) },
    ] };
}
async function mockPage(page: import('@playwright/test').Page, fail = false) {
    await page.route('**/status', route => route.fulfill({ contentType: 'text/html', body: readFileSync('../internal/server/handlers/status.html', 'utf8') }));
    await page.route('**/api/v1/site/config', route => route.fulfill({ json: { data: { site_name: 'Switchyard' } } }));
    await page.route('**/api/v1/public/channel/stats', route => route.fulfill(fail ? { status: 503, json: { message: 'unavailable' } } : { json: { data: snapshot() } }));
    await page.goto('/status');
}
for (const width of [360, 390, 1280]) {
    test('anonymous status page renders safe model data at ' + width, async ({ page }) => {
        await page.setViewportSize({ width, height: 844 });
        const errors: string[] = [];
        page.on('pageerror', error => errors.push(error.message));
        await mockPage(page);
        await expect(page.locator('[data-testid="model-card"]')).toHaveCount(2);
        await expect(page.locator('body')).toContainText('#1001');
        await expect(page.locator('body')).toContainText('alpha-model');
        const priced = page.locator('[data-testid="model-card"]').filter({ hasText: 'alpha-model' });
        await expect(priced).toContainText('0.60×');
        await expect(priced.locator('.bucket')).toHaveCount(15);
        const idle = page.locator('[data-testid="model-card"]').filter({ hasText: '#1002' });
        await expect(idle).toContainText('—');
        await expect(idle).not.toContainText('0%');
        await expect(idle.locator('.pill')).toContainText('无样本');
        await expect(page.locator('body')).toContainText('<img src=x onerror=alert(1)>');
        await expect(page.locator('img[src="x"]')).toHaveCount(0);
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
        await page.locator('#search').fill('alpha');
        await expect(page.locator('[data-testid="model-card"]')).toHaveCount(1);
        await page.locator('#search').fill('not-found');
        await expect(page.locator('[data-testid="model-card"]')).toHaveCount(0);
        expect(errors).toEqual([]);
    });
}
test('status supports themes, language, filters and table view', async ({ page }) => {
    await mockPage(page);
    await expect(page.locator('[data-testid="model-card"]')).toHaveCount(2);
    await page.locator('#status-filter').selectOption('none');
    await expect(page.locator('[data-testid="model-card"]')).toHaveCount(1);
    await expect(page.locator('[data-testid="model-card"]')).toContainText('#1002');
    await page.locator('#status-filter').selectOption('all');
    await page.locator('#theme').click();
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
    await page.locator('#theme').click();
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
    await page.locator('#langEn').click();
    await expect(page.locator('html')).toHaveAttribute('lang', 'en');
    await page.locator('#viewTable').click();
    await expect(page.locator('[data-testid="model-row"]')).toHaveCount(2);
});
test('status text remains legible in light and dark themes', async ({ page }) => {
    await mockPage(page);
    await expect(page.locator('[data-testid="model-card"]')).toHaveCount(2);
    for (const theme of ['light', 'dark']) {
        await page.evaluate(t => document.documentElement.setAttribute('data-theme', t), theme);
        const contrast = await page.evaluate(() => {
            const rgb = (s: string) => (s.match(/[0-9.]+/g) || []).slice(0, 3).map(Number).map(v => {
                const c = v / 255; return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
            });
            const luminance = (s: string) => { const c = rgb(s); return .2126*c[0] + .7152*c[1] + .0722*c[2]; };
            const style = getComputedStyle(document.body);
            const a = luminance(style.color), b = luminance(style.backgroundColor);
            return (Math.max(a,b)+.05)/(Math.min(a,b)+.05);
        });
        expect(contrast).toBeGreaterThanOrEqual(4.5);
    }
});
test('empty public inventory is distinct from a fetch failure', async ({ page }) => {
    await mockPage(page);
    await expect(page.locator('[data-testid="model-card"]')).toHaveCount(2);
    await page.route('**/api/v1/public/channel/stats', route => route.fulfill({ json: { data: { ...snapshot(), models: [] } } }));
    await page.locator('#refresh').click();
    await expect(page.locator('[data-testid="model-card"]')).toHaveCount(0);
    await expect(page.locator('body')).toContainText('暂无公开数据');
    await expect(page.getByRole('alert')).toBeHidden();
});
test('status preserves last snapshot when a refresh fails', async ({ page }) => {
    await mockPage(page);
    await expect(page.locator('[data-testid="model-card"]')).toHaveCount(2);
    await page.route('**/api/v1/public/channel/stats', route => route.fulfill({ status: 503, json: {} }));
    await page.locator('#refresh').click();
    await expect(page.getByRole('alert')).toBeVisible();
    await expect(page.getByRole('alert')).toContainText('最近成功快照');
    await expect(page.locator('[data-testid="model-card"]')).toHaveCount(2);
});
test('status refreshes the rolling window after fifteen seconds', async ({ page }) => {
    await page.clock.install();
    let calls = 0;
    await mockPage(page);
    await expect(page.locator('[data-testid="model-card"]')).toHaveCount(2);
    await page.route('**/api/v1/public/channel/stats', route => {
        calls++;
        return route.fulfill({ json: { data: snapshot() } });
    });
    await page.clock.fastForward(16000);
    await expect.poll(() => calls).toBeGreaterThan(0);
});
test('public status reports fetch failures and recovers on retry', async ({ page }) => {
    await mockPage(page, true);
    await expect(page.getByRole('alert')).toBeVisible();
    await page.route('**/api/v1/public/channel/stats', route => route.fulfill({ json: { data: snapshot() } }));
    await page.locator('#refresh').click();
    await expect(page.locator('[data-testid="model-card"]')).toHaveCount(2);
});
