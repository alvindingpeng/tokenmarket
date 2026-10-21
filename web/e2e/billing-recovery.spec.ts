import { test, expect, type Page } from '@playwright/test';

async function openRecovery(page: Page, fail = false, runFails = false) {
    await page.addInitScript(() => {
        localStorage.setItem('nav-storage', JSON.stringify({ state: { currentPage: 'ops', direction: 0, settingSection: 'personal' }, version: 0 }));
    });
    let runs = 0;
    await page.route('**/api/v1/**', async route => {
        const path = new URL(route.request().url()).pathname;
        if (path === '/api/v1/user/status') return route.fulfill({ json: { data: { id: 1, username: 'test-admin', role: 'admin', status: 'active', balance: 0 } } });
        if (path.endsWith('/billing-recovery/list')) return route.fulfill(fail ? { status: 503, json: { message: 'unavailable' } } : { json: { data: { items: [{ id: 1, reservation_id: 8, request_id: '9223372036854775807', user_id: 2, status: 'manual', attempts: 8, next_attempt_at: null, last_error: '<script>alert(1)</script>', created_at: '', updated_at: '' }] } } });
        if (path.endsWith('/billing-recovery/run')) { runs++; return route.fulfill(runFails ? { status: 500, json: { message: 'partial processing failure' } } : { json: { data: { accepted: true } } }); }
        if (path.endsWith('/overview')) return route.fulfill({ json: { data: {} } });
        return route.fulfill({ json: { data: { items: [] } } });
    });
    await page.goto('/');
    await page.getByRole('button', { name: 'Ops', exact: true }).click();
    await page.getByRole('tab', { name: /结算补偿|結算補償|Settlement recovery/ }).click();
    return () => runs;
}

for (const width of [390, 1280]) {
    test('recovery queue is readable and safe at ' + width, async ({ page }) => {
        await page.setViewportSize({ width, height: 844 });
        const runs = await openRecovery(page);
        await expect(page.getByText('9223372036854775807', { exact: true })).toBeVisible();
        await expect(page.getByText('<script>alert(1)</script>', { exact: true })).toBeVisible();
        await expect(page.getByText(/^(人工核查|Manual review)$/)).toBeVisible();
        await page.getByRole('button', { name: /处理到期任务|處理到期任務|Process due tasks/ }).click();
        await expect.poll(runs).toBe(1);
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
        await expect(page.locator('button').filter({ hasText: /强制|強制|Force/ })).toHaveCount(0);
    });
}

test('partial recovery run failure keeps queue visible and warns of uncertain results', async ({ page }) => {
    const runs = await openRecovery(page, false, true);
    await page.getByRole('button', { name: /处理到期任务|處理到期任務|Process due tasks/ }).click();
    await expect.poll(runs).toBe(1);
    await expect(page.getByText(/不能据此假设|不能據此假設|do not assume no transactions/)).toBeVisible();
    await expect(page.getByText('9223372036854775807', { exact: true })).toBeVisible();
});

test('failed refresh preserves last financial task snapshot and signals uncertainty', async ({ page }) => {
    await openRecovery(page);
    await expect(page.getByText('9223372036854775807', { exact: true })).toBeVisible();
    await page.route('**/api/v1/ops/billing-recovery/list?*', route => route.fulfill({ status: 503, json: { message: 'unavailable' } }));
    await page.getByRole('button', { name: /^(刷新|重新整理|Refresh)$/ }).click();
    await expect(page.getByRole('alert')).toContainText(/加载失败|載入失敗|Could not load/, { timeout: 20000 });
    await expect(page.getByText('9223372036854775807', { exact: true })).toBeVisible();
    await expect(page.getByText(/暂无结算补偿记录|暫無結算補償紀錄|No recovery tasks recorded/)).toHaveCount(0);
});

test('recovery fetch failure is not shown as an empty queue', async ({ page }) => {
    await openRecovery(page, true);
    await expect(page.getByRole('alert')).toContainText(/加载失败|載入失敗|Could not load/, { timeout: 20000 });
    await expect(page.getByText(/暂无结算补偿记录|暫無結算補償紀錄|No recovery tasks recorded/)).toHaveCount(0);
});
