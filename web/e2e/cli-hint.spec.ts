import { expect, test } from '@playwright/test';

// CLI 取凭据的导入引导: 验证界面**真的显示**了正确的命令与路径,
// 以及**真的隐藏**了没有 CLI 的平台。
//
// 为什么必须跑浏览器: 这些是纯渲染行为 —— 数据表单测能钉住 conformance,
// 但「选哪个平台时显不显示」只有真的点一次才知道。
//
// 反向断言(kiro/gemini/grok 不显示)和正向一样重要:
// 显示了不该显示的引导, 会让人以为这些平台也能用 CLI 登录, 然后照着做且失败。

test.beforeEach(async ({ page }) => {
    // 服务端只认 auth cookie(不是 Authorization 头), 所以必须经 context 注入。
    const token = process.env.SWITCHYARD_E2E_JWT;
    test.skip(!token, '缺少 SWITCHYARD_E2E_JWT');
    await page.context().addCookies([{
        name: 'auth',
        value: token!,
        domain: '127.0.0.1',
        path: '/',
    }]);
});

const CLI_HINT_TITLE = /如何获取这份凭据|How to get this credential/;

test('导入对话框: 选 claude 时显示本机 CLI 引导', async ({ page }) => {
    await page.goto('/');
    await page.getByRole('button', { name: 'Channel' }).click();
    await page.getByRole('button', { name: /^(导入账号|Import account)$/ }).click();

    await expect(page.getByText(CLI_HINT_TITLE)).toBeVisible();

    // 默认平台是 codex, 先验 codex 的引导。
    await expect(page.getByText('codex login')).toBeVisible();
    await expect(page.getByText('~/.codex/auth.json')).toBeVisible();

    // 切到 claude: 命令必须是 claude auth login, 不是 claude
    // (只写 claude 会进交互式会话, 不触发登录)。
    await page.getByRole('button', { name: 'claude', exact: true }).click();
    await expect(page.getByText('claude auth login')).toBeVisible();
    await expect(page.getByText('~/.claude/.credentials.json')).toBeVisible();
});

test('导入对话框: 没有官方 CLI 的平台不显示引导', async ({ page }) => {
    await page.goto('/');
    await page.getByRole('button', { name: 'Channel' }).click();
    await page.getByRole('button', { name: /^(导入账号|Import account)$/ }).click();

    // kiro 没有官方 CLI; gemini 的 CLI 产出 OAuth 令牌而我们吃 Cookie;
    // grok 未核实。三个都不该出现引导。
    for (const platform of ['kiro', 'gemini', 'grok']) {
        await page.getByRole('button', { name: platform, exact: true }).click();
        await expect(page.getByText(CLI_HINT_TITLE)).toBeHidden();
    }

    // 切回有引导的平台, 确认是「按平台切换」而不是「打开过一次就永久消失」。
    await page.getByRole('button', { name: 'codex', exact: true }).click();
    await expect(page.getByText(CLI_HINT_TITLE)).toBeVisible();
});
// 本项目的 e2e 专治一类缺陷: 容器塌陷 -> overflow-hidden 裁剪 -> 内容看不见也滚不到。
// CliHint 是弹层里新加的一块, 必须确认它真的在视口内可点可见, 而不是被裁掉。
// 判据用 elementFromPoint 而不是 boundingBox: 后者在被裁剪时同样显示正常。
test('CliHint 布局: 不被裁剪且内容可见', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.goto('/');
    await page.getByRole('button', { name: 'Channel' }).click();
    await page.getByRole('button', { name: /^(导入账号|Import account)$/ }).click();
    await page.getByRole('button', { name: 'claude', exact: true }).click();

    const box = await page.getByText(CLI_HINT_TITLE).boundingBox();
    expect(box).not.toBeNull();
    expect(box!.height).toBeGreaterThan(0);
    expect(box!.y).toBeGreaterThanOrEqual(0);
    expect(box!.y + box!.height).toBeLessThanOrEqual(900);

    const hit = await page.evaluate(({ x, y }) => {
        const el = document.elementFromPoint(x, y);
        return el ? el.textContent?.slice(0, 40) : null;
    }, { x: box!.x + box!.width / 2, y: box!.y + box!.height / 2 });
    expect(hit).toContain('如何获取这份凭据');

    // Windows 兜底路径也要在, 否则 Windows 用户不知道该去哪。
    await expect(page.getByText(/Windows 上是|On Windows/)).toBeVisible();
});
