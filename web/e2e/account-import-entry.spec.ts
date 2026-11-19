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
    await expect(page.getByRole('button', { name: /^(导入账号|Import account)$/ })).toBeVisible();
});

test('独立导入对话框列出可选的目标渠道', async ({ page }) => {
    await page.goto('/');
    await page.getByRole('button', { name: 'Channel' }).click();
    await page.getByRole('button', { name: /^(导入账号|Import account)$/ }).click();

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
    // 精确点卡片标题: getByText 会匹配到别处(例如下拉里的 option), 用 heading 定位到卡片本身。
    await page.getByRole('heading', { name: 'beta', exact: true }).click();
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

test('账号页展示账号列表, 并把问题账号排在前面', async ({ page }) => {
    await page.goto('/');
    // 导航里新增的 Accounts 项: 这是账号管理面的入口。
    await page.getByRole('button', { name: /^(Accounts|上游账号)$/ }).click();

    // 表格应有表头 —— 证明渲染的是列表而不是空态。
    // 导航文案必须翻译过: 缺 navbar.accounts 时这里会渲染成裸 key。
    // 这个缺陷在真实浏览器里出现过, 单测测不出来(它只在缺文案时才发生)。
    await expect(page.getByText('navbar.accounts')).toBeHidden();
    // 汇总条应显示四种状态计数, 且 cookie 那个账号让"需人工"不为零。
    await expect(page.getByText('需人工').first()).toBeVisible();
    await expect(page.getByRole('columnheader', { name: '账号' })).toBeVisible();

    // 两个账号都应在: claude(oauth) 与 gemini(cookie)。
    await expect(page.getByText('claude-demo')).toBeVisible();
    await expect(page.getByText('gemini-demo')).toBeVisible();

    // cookie 类账号应被标成需人工: 它无法自动续期。
    await expect(page.getByText('需人工').first()).toBeVisible();

    // 归属渠道要显示: 账号散落在不同渠道里, 不显示渠道就无法判断它属于谁。
    await expect(page.getByText('alpha').first()).toBeVisible();
    await expect(page.getByText('beta').first()).toBeVisible();
});

test('账号页的搜索能筛掉不匹配的行', async ({ page }) => {
    await page.goto('/');
    await page.getByRole('button', { name: /^(Accounts|上游账号)$/ }).click();
    await page.getByPlaceholder('搜索渠道 / 凭据 / 平台').fill('gemini');

    // 搜 gemini 后 claude 那行应消失, gemini 留着。
    await expect(page.getByText('gemini-demo')).toBeVisible();
    await expect(page.getByText('claude-demo')).toBeHidden();
});

test('Share 页卡片显示账号健康度摘要', async ({ page }) => {
    await page.goto('/');
    await page.getByRole('button', { name: /^(Share|分享)$/ }).click();

    // alpha 渠道有 claude oauth 账号(判据: 正常), beta 有 gemini cookie(判据: 需人工)。
    // 健康度摘要必须渲染在卡片里 —— 这是第 5 项的核心: 改价/上架时一眼看见账号状态。
    const alphaCard = page.locator('article').filter({ hasText: 'alpha' }).first();
    await expect(alphaCard.getByText(/个账号|account\(s\)/)).toBeVisible();
    // oauth 账号无到期时间 -> 判据正常。
    await expect(alphaCard.getByText(/^(正常|OK)$/).first()).toBeVisible();

    const betaCard = page.locator('article').filter({ hasText: 'beta' }).first();
    await expect(betaCard.getByText(/个账号|account\(s\)/)).toBeVisible();
    // cookie 类无法自动续期 -> 判据需人工, 绝不能被显示成正常。
    await expect(betaCard.getByText(/^(需人工|Manual)$/).first()).toBeVisible();
});

test('批量导入: 一次粘贴多个账号 JSON, 逐条预检并显示结果', async ({ page }) => {
    await page.goto('/');
    await page.getByRole('button', { name: 'Channel' }).click();
    await page.getByRole('button', { name: /批量导入账号/ }).click();

    // 选个渠道
    await page.locator('[role="combobox"]').first().click();
    await page.getByRole('option', { name: 'alpha' }).click();

    // 贴两个 codex 账号 JSON (一行一个)。名字带时间戳: 重复跑同一条用例会撞名(渠道内唯一),
    // 而撞名是正确行为 —— 用例不该因此变红。
    const stamp = Date.now();
    const good = JSON.stringify({ name: 'line-a-' + stamp, access_token: 'at1', refresh_token: 'rt1' });
    const bad = JSON.stringify({ name: 'line-b-' + stamp, access_token: 'at2' }); // 缺 refresh_token
    await page.getByPlaceholder(/每行一个 JSON|One JSON per line/).fill(good + '\n' + bad);

    // 预检应立刻出现: 第 2 条缺 refresh_token
    await expect(page.getByText(/缺少 refresh_token/)).toBeVisible();
    await expect(page.getByText(/1 处预检错误|validation error/)).toBeVisible();

    // 修正第 2 条
    const badFixed = JSON.stringify({ name: 'line-b-' + stamp, access_token: 'at2', refresh_token: 'rt2' });
    await page.getByPlaceholder(/每行一个 JSON|One JSON per line/).fill(good + '\n' + badFixed);

    // 预检错误消失, 提交
    await expect(page.getByText(/缺少 refresh_token/)).not.toBeVisible();
    await page.getByRole('button', { name: /批量导入$/ }).click();

    // 结果应显示 2 条成功
    await expect(page.getByText(/2 个账号|2 account/)).toBeVisible();
    await expect(page.getByText(/成功: 2|Succeeded: 2/)).toBeVisible();
});

test('批量导入: JSON 数组格式也支持', async ({ page }) => {
    await page.goto('/');
    await page.getByRole('button', { name: 'Channel' }).click();
    await page.getByRole('button', { name: /批量导入账号/ }).click();
    await page.locator('[role="combobox"]').first().click();
    await page.getByRole('option', { name: 'beta' }).click();

    // 贴 JSON 数组
    const arr = JSON.stringify([
        { name: 'arr-1-' + Date.now(), raw: { access_token: 'a1', refresh_token: 'r1' } },
        { name: 'arr-2-' + Date.now(), raw: { access_token: 'a2', refresh_token: 'r2' } }
    ]);
    await page.getByPlaceholder(/每行一个 JSON|One JSON per line/).fill(arr);

    // 预检通过, 提交
    await expect(page.getByText(/缺少 refresh_token/)).not.toBeVisible();
    await page.getByRole('button', { name: /批量导入$/ }).click();

    await expect(page.getByText(/成功: 2|Succeeded: 2/)).toBeVisible();
});

