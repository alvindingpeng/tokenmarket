import { expect, test, type Page } from '@playwright/test';

async function openConsole(page: Page) {
    await page.addInitScript(() => {
        localStorage.setItem('switchyard-settings', JSON.stringify({ state: { locale: 'en' }, version: 0 }));
    });
    await page.route('**/api/v1/**', async (route) => {
        const path = new URL(route.request().url()).pathname;
        const data = path === '/api/v1/user/status'
            ? { id: 1, username: 'layout-test', role: 'admin', status: 'active', balance: 100 }
            : path.includes('stats') ? {} : [];
        await route.fulfill({ json: { data } });
    });
    await page.goto('/');
    await expect(page.getByRole('navigation', { name: 'Main Navigation' })).toBeVisible();
}

test.use({ reducedMotion: 'reduce' });

for (const viewport of [{ width: 1440, height: 900 }, { width: 820, height: 1180 }, { width: 320, height: 600 }]) {
    test(`console frame and navigation fit at ${viewport.width}px`, async ({ page }) => {
        await page.setViewportSize(viewport);
        await openConsole(page);
        const dock = page.getByRole('navigation', { name: 'Main Navigation' });
        await dock.getByRole('button', { name: 'Tickets', exact: true }).click();
        await expect(dock.getByRole('button', { name: 'Tickets', exact: true })).toHaveAttribute('aria-current', 'page');
        await dock.getByRole('button', { name: 'Group', exact: true }).click();
        const main = page.getByRole('main');
        const title = page.locator('header h1');
        await expect(title).toHaveText('Model Config');
        expect((await title.boundingBox())?.width).toBeGreaterThan(40);

        const search = page.getByRole('button', { name: 'Search', exact: true });
        await search.click();
        await page.getByRole('textbox', { name: 'Search', exact: true }).fill('long model name');

        const mainBox = (await main.boundingBox())!;
        const dockBox = (await dock.boundingBox())!;
        expect(viewport.width - mainBox.x - mainBox.width).toBeLessThanOrEqual(24);
        if (viewport.width < 768) {
            expect(mainBox.y + mainBox.height).toBeLessThanOrEqual(dockBox.y);
        } else {
            expect(dockBox.x + dockBox.width).toBeLessThan(mainBox.x);
        }
        expect(await title.evaluate((element) => {
            const header = element.closest('header')!;
            return header.scrollWidth <= header.clientWidth && document.documentElement.scrollWidth <= innerWidth;
        })).toBe(true);
    });
}

test('white template persists and respects reduced motion', async ({ page }) => {
    await page.emulateMedia({ colorScheme: 'dark' });
    await openConsole(page);
    const dock = page.getByRole('navigation', { name: 'Main Navigation' });
    await dock.getByRole('button', { name: 'Setting', exact: true }).click();
    const theme = page.getByRole('combobox', { name: 'Theme', exact: true });
    await theme.click();
    await page.getByRole('option', { name: 'White template' }).click();
    const root = page.locator('html');
    await expect(root).toHaveClass(/theme-white/);
    await expect(root).toHaveCSS('color-scheme', 'light');
    await page.reload();
    await expect(theme).toHaveText('White template');
    await dock.getByRole('button', { name: 'Home', exact: true }).click();
    await expect(page.locator('.home-summary-card').first()).toHaveCSS('animation-name', 'none');
    await expect(page.locator('body')).toHaveCSS('background-color', 'rgb(247, 248, 250)');
});

test('compact workspace keeps navigation and summary usable across screen sizes', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('theme', 'compact'));
    await page.setViewportSize({ width: 1440, height: 900 });
    await openConsole(page);
    const dock = page.getByRole('navigation', { name: 'Main Navigation' });
    await expect(page.locator('html')).toHaveClass(/theme-compact/);
    await expect(dock.getByRole('button', { name: 'Home', exact: true }).locator('.console-nav-label')).toBeVisible();
    await expect(page.locator('.home-summary-card')).toHaveCount(4);
    await dock.getByRole('button', { name: 'Setting', exact: true }).click();
    await expect(page.getByRole('combobox', { name: 'Theme' })).toHaveText('Compact workspace');
    await page.reload();
    await expect(page.locator('html')).toHaveClass(/theme-compact/);
    await page.setViewportSize({ width: 320, height: 700 });
    await dock.getByRole('button', { name: 'Home', exact: true }).click();
    await expect(page.locator('.home-summary-card')).toHaveCount(4);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320);
    const mainBox = (await page.getByRole('main').boundingBox())!;
    const dockBox = (await dock.boundingBox())!;
    expect(mainBox.y + mainBox.height).toBeLessThanOrEqual(dockBox.y);
});
