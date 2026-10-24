import { test, expect, type Locator, type Page } from '@playwright/test';

// 高危操作二次验证(step-up)弹窗的端到端契约: 真实浏览器 + 每个用例自己隔离的路由 mock,
// 不连任何真实后端。红线与 web/src/api/step-up.test.ts 的单测一致, 但这里验的是
// 「真的弹了框、真的点了按钮、网络请求到底发了几次」:
//
//   1. 高危请求收到 428 + error_code=step_up_required 才弹密码确认;
//   2. 口令不对: 服务端说明就地显示, 业务请求绝不重发;
//   3. 口令正确: 换凭证后把原请求原样重发一次(附 X-Step-Up-Token), 且只重发这一次;
//   4. 取消: 弹窗关掉, 业务请求一次都不补发;
//   5. 手机宽度下弹窗可用: 不横向溢出, 两个按钮都真的点得到;
//   6. 竞态(在途验证 × Escape/取消): 取消生效后, 迟到的验证响应换来的凭证必须整体作废,
//      不得授权任何后一个请求; 若实现规定忙时禁止 Escape, 则断言它确实不会取消;
//   7. 竞态(迟到结果 × 新一代确认): 被取消那一代的迟到验证结果(成功或 401)不得改写
//      新一代确认的忙态/错误态 —— 新弹窗必须保持自己的 busy 与输入状态。
//
// 计数与请求头记录都在 Node 侧的 route 回调里维护, 不依赖页面内状态 ——
// 断言的是浏览器真实发出的网络往返, 页面想藏也藏不掉。

type Call = { path: string; method: string; token: string | null; body: string };

const CORRECT_PASSWORD = 'correct-horse-battery-staple';
const STEP_UP_TOKEN = 'step-token-1';

// hitTest 判定元素中心点是否真的命中自己: 被裁剪/被遮挡/pointer-events:none 时
// boundingBox 都「看起来正常」, 只有 document.elementFromPoint 才证明用户点得到(见 e2e/README.md)。
async function hitTest(target: Locator) {
    return target.evaluate((element) => {
        const rect = element.getBoundingClientRect();
        const hit = document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2);
        return hit === element || (hit !== null && element.contains(hit));
    });
}

// openRecovery 进入 运维 > 结算补偿, 并给每一次网络往返记账:
// 高危请求(计费恢复 run)没有 X-Step-Up-Token 就回 428 + step_up_required(明确表示「没进业务处理」),
// 带上有效凭证才算执行成功; 换凭证接口按口令对错回短期凭证或 401。
async function openRecovery(page: Page) {
    const runCalls: Call[] = [];
    const verifyCalls: Call[] = [];
    const allCalls: Call[] = [];
    // verifyGate 让「换凭证」的响应可控地迟到: hold() 之后的验证响应一律挂起,
    // releaseNext() 按先来后到逐个放行 —— 用来制造「验证在途时取消」「迟到响应撞上新一代确认」
    // 这类竞态(多个在途验证可同时被挂起)。
    const verifyGate = {
        armed: false,
        held: [] as Array<() => void>,
        hold() { this.armed = true; },
        releaseNext() { this.held.shift()?.(); },
    };
    await page.addInitScript(() => {
        localStorage.setItem('nav-storage', JSON.stringify({ state: { currentPage: 'ops', direction: 0, settingSection: 'personal' }, version: 0 }));
    });
    await page.route('**/api/v1/**', async route => {
        const request = route.request();
        const path = new URL(request.url()).pathname;
        const call: Call = { path, method: request.method(), token: request.headers()['x-step-up-token'] ?? null, body: request.postData() ?? '' };
        allCalls.push(call);
        if (path === '/api/v1/user/status') return route.fulfill({ json: { data: { id: 1, username: 'test-admin', role: 'admin', status: 'active', balance: 0 } } });
        if (path === '/api/v1/user/step-up') {
            verifyCalls.push(call);
            // 成功/失败响应都可被挂起: 迟到的成功、迟到的 401 都是竞态素材。
            if (verifyGate.armed) await new Promise<void>((resolve) => { verifyGate.held.push(resolve); });
            const password = (JSON.parse(call.body || '{}') as { password?: string }).password;
            try {
                return password === CORRECT_PASSWORD
                    ? await route.fulfill({ json: { data: { token: STEP_UP_TOKEN, expires_in: 300 } } })
                    : await route.fulfill({ status: 401, json: { message: 'Incorrect password' } });
            } catch {
                // 页面在放行前中止了这次验证(实现若改为取消时中断在途请求): 响应无处投递, 忽略。
                return;
            }
        }
        if (path.endsWith('/billing-recovery/run')) {
            runCalls.push(call);
            return call.token
                ? route.fulfill({ json: { data: { accepted: true } } })
                : route.fulfill({ status: 428, json: { code: 428, message: 'Step-up verification required', error_code: 'step_up_required' } });
        }
        if (path.endsWith('/billing-recovery/list')) return route.fulfill({ json: { data: { items: [{ id: 1, reservation_id: 8, request_id: '9223372036854775807', user_id: 2, status: 'pending', attempts: 1, next_attempt_at: null, last_error: '', created_at: '', updated_at: '' }] } } });
        if (path.endsWith('/overview')) return route.fulfill({ json: { data: {} } });
        return route.fulfill({ json: { data: { items: [] } } });
    });
    await page.goto('/');
    await page.getByRole('button', { name: 'Ops', exact: true }).click();
    await page.getByRole('tab', { name: /结算补偿|結算補償|Settlement recovery/ }).click();
    return { runCalls, verifyCalls, allCalls, verifyGate };
}

const stepUpDialog = (page: Page) => page.getByRole('dialog', { name: /高危操作确认|高危操作確認|Confirm it's you/ });
const runButton = (page: Page) => page.getByRole('button', { name: /处理到期任务|處理到期任務|Process due tasks/ });
// confirmButton 同时匹配闲态/忙态文案(忙时按钮显示「验证中…」), 断言忙态时也能定位到它。
const confirmButton = (page: Page) => stepUpDialog(page).getByRole('button', { name: /确认并继续|確認並繼續|Confirm and continue|验证中|驗證中|Verifying/ });
const cancelButton = (page: Page) => stepUpDialog(page).getByRole('button', { name: /^(取消|Cancel)$/ });

for (const width of [390, 1280]) {
    test('428 弹出密码确认, 取消后高危请求一次都不补发 at ' + width, async ({ page }) => {
        await page.setViewportSize({ width, height: 844 });
        const { runCalls, verifyCalls } = await openRecovery(page);

        await runButton(page).click();
        const dialog = stepUpDialog(page);
        await expect(dialog).toBeVisible();
        await expect(dialog).toContainText(/这个操作会改动资金|這個操作會改動資金|This action changes money/);
        // 弹窗只在「被拦下的那一次」之后出现: 业务请求一次, 且没带凭证。
        expect(runCalls).toHaveLength(1);
        expect(runCalls[0].token).toBeNull();
        expect(verifyCalls).toHaveLength(0);

        // 未输密码时确认键不可用(禁用态 pointer-events:none, 对点击透明是设计行为)。
        await expect(confirmButton(page)).toBeDisabled();
        // 移动端可用: 不横向溢出; 输完密码后取消/确认两个按钮中心点都命中自己(真的点得到)。
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
        await stepUpDialog(page).locator('input[type="password"]').fill('typed-then-cancelled');
        expect(await hitTest(cancelButton(page))).toBe(true);
        expect(await hitTest(confirmButton(page))).toBe(true);

        await cancelButton(page).click();
        // 高危操作以失败收场(onError 提示)之后再数请求: 取消 = 不执行, 也不去换凭证。
        await expect(page.getByText(/处理失败|處理失敗|Recovery failed/)).toBeVisible();
        await expect(dialog).toHaveCount(0);
        expect(runCalls).toHaveLength(1);
        expect(verifyCalls).toHaveLength(0);
    });

    test('错误密码不重发业务请求, 正确密码只重发一次并附 X-Step-Up-Token at ' + width, async ({ page }) => {
        await page.setViewportSize({ width, height: 844 });
        const { runCalls, verifyCalls } = await openRecovery(page);

        await runButton(page).click();
        const dialog = stepUpDialog(page);
        await expect(dialog).toBeVisible();
        const input = dialog.locator('input[type="password"]');

        // 口令不对: 服务端说明就地显示, 弹窗留着重试, 业务请求绝不重发。
        await input.fill('wrong-password');
        await confirmButton(page).click();
        await expect(dialog.getByText('Incorrect password')).toBeVisible();
        await expect(input).toHaveAttribute('aria-invalid', 'true');
        await expect(dialog).toBeVisible();
        expect(verifyCalls).toHaveLength(1);
        expect(runCalls).toHaveLength(1);

        // 口令正确: 换凭证后把原请求原样重发一次 —— 载荷一字不改, 且只有这一次。
        await input.fill(CORRECT_PASSWORD);
        await confirmButton(page).click();
        await expect(dialog).toHaveCount(0);
        await expect(page.getByText(/本轮处理结束|本輪處理結束|Processing finished/)).toBeVisible();

        expect(verifyCalls).toHaveLength(2);
        expect(runCalls).toHaveLength(2);
        expect(runCalls[0].token).toBeNull();
        expect(runCalls[1].token).toBe(STEP_UP_TOKEN);
        expect(runCalls[1].method).toBe('POST');
        expect(runCalls[1].body).toBe(runCalls[0].body);
    });

    test('在途验证中按 Escape 的取消/放行竞态: 取消则迟到凭证作废, 不取消则照常只重发一次 at ' + width, async ({ page }) => {
        await page.setViewportSize({ width, height: 844 });
        const { runCalls, verifyCalls, allCalls, verifyGate } = await openRecovery(page);

        await runButton(page).click();
        const dialog = stepUpDialog(page);
        await expect(dialog).toBeVisible();
        const input = dialog.locator('input[type="password"]');
        await input.fill(CORRECT_PASSWORD);

        // 挂起换凭证的响应, 造出「验证在途」的竞态窗口。
        verifyGate.hold();
        await confirmButton(page).click();
        await expect.poll(() => verifyCalls.length).toBe(1); // 验证请求已发出, 响应还没回来
        expect(runCalls).toHaveLength(1);

        // 竞态动作: 验证还在路上时按 Escape(取消键此刻被 busy 禁用, Escape 是唯一的在途取消入口)。
        await input.press('Escape');
        await page.evaluate(() => new Promise((resolve) => setTimeout(resolve, 50))); // 给一帧观察取消是否即时生效
        const cancelledWhileBusy = (await dialog.count()) === 0;

        // 迟到的验证响应此刻才放行: 它属于哪一代确认, 由实现的取消语义决定。
        const lateResponse = page.waitForResponse((response) => response.url().includes('/api/v1/user/step-up'), { timeout: 5000 }).catch(() => null);
        verifyGate.releaseNext();
        await (await lateResponse)?.finished().catch(() => undefined);
        await page.evaluate(() => new Promise((resolve) => setTimeout(resolve, 150))); // 让页面把迟到响应消化完

        // 判定这次 Escape 的最终语义: 放行后有没有发生「带凭证的重发」。
        const resendConfirmed = await expect
            .poll(() => runCalls.some((call) => call.token === STEP_UP_TOKEN), { timeout: 1500, intervals: [100] })
            .toBe(true)
            .then(() => true)
            .catch(() => false);

        if (resendConfirmed) {
            // 语义 A: 忙时 Escape 不生效 —— 断言不会取消: 弹窗没在 Escape 时离场,
            // 原请求继续在途, 放行后照常只重发一次并附凭证。
            expect(cancelledWhileBusy).toBe(false);
            await expect(dialog).toHaveCount(0);
            await expect(page.getByText(/本轮处理结束|本輪處理結束|Processing finished/)).toBeVisible();
            expect(verifyCalls).toHaveLength(1);
            expect(runCalls).toHaveLength(2);
            expect(runCalls[0].token).toBeNull();
            expect(runCalls[1].token).toBe(STEP_UP_TOKEN);
            expect(runCalls[1].body).toBe(runCalls[0].body); // 原样重发
        } else {
            // 语义 B: 取消生效(即时取消, 或「响应后才取消」都算)。红线:
            // 用户既然取消了, 那次验证换来的凭证必须整体作废 —— 迟到的响应不得授权任何后一个请求。
            await expect(dialog).toHaveCount(0);
            expect(runCalls).toHaveLength(1); // 被拦下的那次之后, 一次都不补发
            expect(verifyCalls).toHaveLength(1);

            // 重新触发高危请求必须再次撞 428 重新弹框 —— 凭证泄漏的实现会直接 200 放行、不再弹框。
            // 先钉死「这一次不得携带凭证」: 泄漏时失败信息会直接指出收到的那枚迟到凭证。
            await runButton(page).click();
            await expect.poll(() => runCalls.length, { timeout: 5000 }).toBe(2);
            expect(runCalls[1].token).toBeNull();
            await expect(dialog).toBeVisible();
            await cancelButton(page).click();
            await expect(dialog).toHaveCount(0);

            // 迟到响应即便再晚一步落地, 也不得给任何请求盖章。
            await page.evaluate(() => new Promise((resolve) => setTimeout(resolve, 150)));
            await runButton(page).click();
            await expect.poll(() => runCalls.length, { timeout: 5000 }).toBe(3);
            expect(runCalls[2].token).toBeNull();
            await expect(dialog).toBeVisible();
            expect(verifyCalls).toHaveLength(1); // 更不会自动重新验证
            expect(allCalls.filter((call) => call.token !== null)).toHaveLength(0);
        }
    });

    test('取消后迟到的验证结果不得改写新一代确认的忙态/错误态 at ' + width, async ({ page }) => {
        await page.setViewportSize({ width, height: 844 });
        const { runCalls, verifyCalls, verifyGate } = await openRecovery(page);
        const dialog = stepUpDialog(page);
        const input = dialog.locator('input[type="password"]');

        // 第一代: 确认(口令错, 响应被挂起)后验证在途, Escape 取消。
        await runButton(page).click();
        await expect(dialog).toBeVisible();
        await input.fill('wrong-password');
        verifyGate.hold();
        await confirmButton(page).click();
        await expect.poll(() => verifyCalls.length).toBe(1);
        await input.press('Escape');
        await expect(dialog).toHaveCount(0);

        // 第二代: 新的高危请求重新弹框, 用户重新确认(口令对), 验证同样在途。
        await runButton(page).click();
        await expect(dialog).toBeVisible();
        await input.fill(CORRECT_PASSWORD);
        verifyGate.hold();
        await confirmButton(page).click();
        await expect.poll(() => verifyCalls.length).toBe(2);
        await expect(confirmButton(page)).toBeDisabled(); // 第二代此刻在忙: 确认键不可用
        await expect(confirmButton(page)).toContainText(/验证中|驗證中|Verifying/);

        // 第一代的迟到 401 此刻才回来: 它属于已被取消的那一代, 不得改写新一代的忙态/错误态。
        const lateError = page.waitForResponse((response) => response.url().includes('/api/v1/user/step-up'), { timeout: 5000 }).catch(() => null);
        verifyGate.releaseNext();
        await (await lateError)?.finished().catch(() => undefined);
        await page.evaluate(() => new Promise((resolve) => setTimeout(resolve, 150))); // 让页面把迟到响应消化完

        await expect(dialog).toBeVisible();
        await expect(dialog.getByText('Incorrect password')).toHaveCount(0); // 迟到的错误不得冒泡进新一代
        await expect(input).not.toHaveAttribute('aria-invalid', 'true');
        await expect(input).toHaveValue(CORRECT_PASSWORD); // 迟到结果不得清掉新一代已输入的口令
        await expect(confirmButton(page)).toBeDisabled(); // 新一代验证仍在途: 忙态不被上一代的收尾改写
        await expect(confirmButton(page)).toContainText(/验证中|驗證中|Verifying/);

        // 第二代自己的响应放行后照常收尾: 只重发一次并附凭证。
        verifyGate.releaseNext();
        await expect(dialog).toHaveCount(0);
        await expect(page.getByText(/本轮处理结束|本輪處理結束|Processing finished/)).toBeVisible();
        expect(verifyCalls).toHaveLength(2);
        expect(runCalls).toHaveLength(3); // 两次 428 + 一次带凭证重发
        expect(runCalls[2].token).toBe(STEP_UP_TOKEN);
        expect(runCalls[2].body).toBe(runCalls[1].body);
    });
}
