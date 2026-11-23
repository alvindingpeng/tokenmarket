import { expect, test } from '@playwright/test';

const catalog = /\/(zh_hans|zh_hant|en)(?:-[\w-]+)?\.json$/;

test('loads only the saved language and fetches another when selected', async ({ page }) => {
    await page.addInitScript(() => {
        localStorage.setItem('switchyard-settings', JSON.stringify({ state: { locale: 'en' }, version: 0 }));
    });
    const requested: string[] = [];
    page.on('request', (request) => {
        const match = new URL(request.url()).pathname.match(catalog);
        if (match && request.resourceType() === 'fetch') requested.push(match[1]);
    });

    await page.goto('/');
    const english = page.getByRole('button', { name: 'EN', exact: true }).first();
    const simplified = page.getByRole('button', { name: '简', exact: true }).first();
    const traditional = page.getByRole('button', { name: '繁', exact: true }).first();
    await expect(english).toBeVisible();
    await expect(page.locator('html')).toHaveAttribute('lang', 'en');
    expect(requested).toEqual(['en']);

    await traditional.click();
    await expect(page.locator('html')).toHaveAttribute('lang', 'zh-Hant');
    await simplified.click();
    await expect(page.locator('html')).toHaveAttribute('lang', 'zh-Hans');
    await english.click();
    await expect(page.locator('html')).toHaveAttribute('lang', 'en');
    expect(requested).toEqual(['en', 'zh_hant', 'zh_hans']);
});

test('keeps the previous language visible when a switch fails', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByRole('button', { name: '简', exact: true }).first()).toBeVisible();
    await page.route(/\/en(?:-[\w-]+)?\.json$/, (route) => route.fulfill({ status: 503 }));
    await page.getByRole('button', { name: 'EN', exact: true }).first().click();
    await expect(page.getByRole('alert')).toContainText('语言加载失败');
    await expect(page.locator('html')).toHaveAttribute('lang', 'zh-Hans');
    await expect(page.getByRole('button', { name: '简', exact: true }).first()).toBeVisible();
});
