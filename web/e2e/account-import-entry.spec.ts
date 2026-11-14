import { expect, test } from '@playwright/test';

// 账号导入的三处入口改动, 用真实浏览器验证。
//
// 单测覆盖不到这三件事: 入口在不在页面上、下拉里有没有渠道、列表里账号显示成什么样。
// 这些全是"看得见/看不见"的问题, 只有真的渲染一次才有答案。
//
// 注意: E2E 环境的界面语言由浏览器/应用默认值决定, 实测是中文。
// 所以这里按中文文案定位, 而不是英文 —— 按英文找会得到"元素不存在",
// 但真正的原因只是语言不是英文, 而不是功能没做出来。

test.beforeEach(async ({ page, baseURL }) => {
    const token = process.env.SWITCHYARD_E2E_JWT;
    test.skip(!token, '缺少 SWITCHYARD_E2E_JWT');
    // 与生产一致: cookie 与源同域后由 Auth 中间件读。
    await page.context().addCookies([{
        name: 'auth', value: token!, url: baseURL!, httpOnly: true, sameSite: 'Lax',
    }]);
});

test('渠道页 Tab 条上有独立的导入入口', async ({ page }) => {
    await page.goto('/');
    await page.getByRole('button', { name: 'Channel' }).click();

    // 入口在 Tab 条右侧, 与"渠道列表/审核队列"同一视觉层级 —— 而不是藏在某张渠道卡片里。
    // 这条断言的意义: 用户不必先钻进某个渠道的编辑表单才能导入账号。
    await expect(page.getByRole('button', { name: '导入账号' })).toBeVisible();
});

test('独立导入对话框列出可选的目标渠道', async ({ page }) => {
    await page.goto('/');
    await page.getByRole('button', { name: 'Channel' }).click();
    await page.getByRole('button', { name: '导入账号' }).click();

    await expect(page.getByText('导入上游账号')).toBeVisible();

    // 目标渠道这一栏只在独立模式下出现, 且必须能选到已存在的渠道。
    await expect(page.getByText('目标渠道')).toBeVisible();
    await page.locator('[role="combobox"]').first().click();
    await expect(page.getByRole('option', { name: 'alpha' })).toBeVisible();
    await expect(page.getByRole('option', { name: 'beta' })).toBeVisible();
});

test('cookie 类账号在凭据列表里标成"需人工"而不是"正常"', async ({ page }) => {
    await page.goto('/');
    await page.getByRole('button', { name: 'Channel' }).click();

    // 进 beta 渠道(gemini cookie 账号挂在它上面)。
    await page.getByText('beta', { exact: true }).first().click();
    // 弹层默认显示统计(利润分析), 凭据列表在两层之下:
    // 先点"编辑"切到编辑视图(普通按钮, 不是 Radix Tab), 再点步骤条上的"凭据"。
    await page.getByRole('button', { name: '编辑' }).click();
    await page.getByRole('button', { name: '凭据' }).click();

    // cookie 类无法自动续期, 必须显示成"需人工"。
    // 若显示成"正常", 使用者会以为系统会一直维持它 —— 而它其实到期就停。
    await expect(page.getByText('需人工').first()).toBeVisible();
    // 平台标识也应在(替代原来那串掩码 JSON)。
    await expect(page.getByText('gemini').first()).toBeVisible();
});
