import { expect, test, type Page } from '@playwright/test';

// 网页登录入口嵌在「导入账号」弹层里。
//
// 这里要钉住的是一件曾经真的坏过的事: 它最初是第二个 MorphingDialog,
// 而外层弹层的关闭判定是 document 级点击外部检测(内容 portal 到 body),
// 于是内层里每一次点击都被外层当成"点了外部", 整个弹窗立刻消失。
// 用户看到的现象是"选个平台窗口就没了", 与"功能没做出来"完全一样。
//
// 所以断言的不是"点两下有没有报错", 而是**弹层还在、且面板已展开** ——
// 只有这两件事同时成立, 才说明嵌套关闭的问题没有回来。
//
// 定位必须限定在登录面板内部: 同一个弹层里"粘贴凭据"那半边也有 claude/gemini
// 按钮, 直接按名字找会命中两个元素(strict mode violation), 那不是功能坏了,
// 是用例没限定范围。

test.beforeEach(async ({ page, baseURL }) => {
    const token = process.env.SWITCHYARD_E2E_JWT;
    test.skip(!token, '缺少 SWITCHYARD_E2E_JWT');
    await page.context().addCookies([{
        name: 'auth', value: token!, url: baseURL!, httpOnly: true, sameSite: 'Lax',
    }]);
});

// 登录面板的根节点: 它由折叠按钮 + 展开后的内容组成, 用按钮定位其祖先容器。
// 这样 panel() 内部的同名按钮不会与粘贴区的那组冲突。
function panel(page: Page) {
    return page
        .getByRole('button', { name: /网页登录导入|Sign in with browser/ })
        .locator('xpath=ancestor::div[contains(@class,"rounded-lg")][1]');
}

async function openDialog(page: Page) {
    await page.goto('/');
    await page.getByRole('button', { name: 'Channel' }).click();
    await page.getByRole('button', { name: /^(导入账号|Import account)$/ }).click();
    await expect(page.getByText(/导入上游账号|Import upstream account/)).toBeVisible();
}

async function openPanel(page: Page) {
    await openDialog(page);
    // 独立导入模式下必须先选目标渠道, 否则面板只会提示先选 —— 那是正确行为。
    await page.locator('[role="combobox"]').first().click();
    await page.getByRole('option').first().click();
    await page.getByRole('button', { name: /网页登录导入|Sign in with browser/ }).click();
}

test('网页登录面板展开时, 导入弹层不会消失', async ({ page }) => {
    await openPanel(page);

    // 弹层必须还在 —— 这正是曾经消失的那个。
    await expect(page.getByText(/导入上游账号|Import upstream account/)).toBeVisible();

    // 面板内容必须真的出现(限定在面板内部, 避开粘贴区同名按钮)。
    await expect(panel(page).getByRole('button', { name: 'claude', exact: true })).toBeVisible();
    await expect(panel(page).getByRole('button', { name: 'gemini', exact: true })).toBeVisible();
    await expect(
        panel(page).getByText(/使用上游 CLI 自带的 OAuth 客户端|uses the vendor CLI/),
    ).toBeVisible();
});

test('在网页登录面板里选平台, 弹层与选择都保持', async ({ page }) => {
    await openPanel(page);

    // 选 gemini: 这是用户报的那一次点击。
    await panel(page).getByRole('button', { name: 'gemini', exact: true }).click();

    await expect(page.getByText(/导入上游账号|Import upstream account/)).toBeVisible();
    await expect(panel(page).getByRole('button', { name: 'gemini', exact: true })).toBeVisible();
    await expect(
        panel(page).getByRole('button', { name: /打开授权页|Open authorization page/ }),
    ).toBeEnabled();
});

test('未选目标渠道时, 登录面板提示先选渠道而不是给出授权入口', async ({ page }) => {
    await openDialog(page);
    // 不选渠道直接展开: 这一步要断言提示存在, 而不是空白或直接报错。
    await page.getByRole('button', { name: /网页登录导入|Sign in with browser/ }).click();

    await expect(
        panel(page).getByText(/请先在上面选择目标渠道|Select the target channel/),
    ).toBeVisible();
    // 不能给出授权按钮: 授权完才发现没渠道, 用户白跑一趟。
    await expect(
        panel(page).getByRole('button', { name: /打开授权页|Open authorization page/ }),
    ).toHaveCount(0);
});

test('网页登录面板只列可登录的平台', async ({ page }) => {
    await openPanel(page);

    // kiro/grok 没有网页登录路径(不是 OAuth 平台, 或端点未核实)。
    await expect(panel(page).getByRole('button', { name: 'kiro', exact: true })).toHaveCount(0);
    await expect(panel(page).getByRole('button', { name: 'grok', exact: true })).toHaveCount(0);
});
