import { expect, test, type Page } from '@playwright/test';

// 布局回归: 弹窗/面板里的「气泡式」塌陷。
//
// 这一族的病是同一个: 一层 CSS 让容器在纵轴塌掉, 外层 min-h 兜住了下限, 内容却按自然高度渲染,
// 超出裁剪层 overflow-hidden 的部分被切掉, 而外层 scrollHeight === clientHeight, 连滚动都没有。
// 结果就是「按钮看不见、也滚不到」—— v0.39.4(限流页清单 2px)、v0.39.5(渠道编辑弹窗保存按钮)都是这一族。
//
// 判据不取「在视口内」, 而是「按钮就是那个坐标最上层的元素」:
// 前者会被遮盖、被裁剪层吃掉的情形骗过, 后者才是用户真的点得到。

// 站点地址一律走相对路径, 由 playwright.config.ts 的 baseURL 决定打哪个实例。
// 早先这里写死了 8088(已部署产物), 结果用例验的是「上次构建有没有问题」:
// 把 flex-1 加回源码它照样绿。改源码必须打到 dev server 才有意义。

// 弹出层滚动层: 这一层才是真正滚动与裁剪的盒子。
const DIALOG = 'div.fixed.inset-0.z-50.overflow-y-auto';

// hitTest 断言元素中心点命中的就是它自己(或其子节点)。
// 用 elementFromPoint 而不是 boundingBox: 被 overflow-hidden 裁掉、被兄弟层盖住、pointer-events 被禁,
// 三种「看得见点不到」都会在 boundingBox 上表现为正常, 只有命中测试能拆穿。
async function expectClickable(page: Page, selector: string, label: string) {
    const info = await page.evaluate(({ selector, label }) => {
        const el = document.querySelector(selector);
        if (!el) return { ok: false, reason: label + ': 元素不存在' };
        const rect = el.getBoundingClientRect();
        if (rect.width === 0 || rect.height === 0) {
            return { ok: false, reason: label + ': 尺寸为 0' };
        }
        const cx = rect.left + rect.width / 2;
        const cy = rect.top + rect.height / 2;
        const hit = document.elementFromPoint(cx, cy);
        if (!hit) return { ok: false, reason: label + ': 中心点没有任何元素(在视口外或被裁剪)' };
        if (hit !== el && !el.contains(hit)) {
            return { ok: false, reason: label + ': 中心点被 ' + (hit.tagName + '.' + String(hit.className).slice(0, 40)) + ' 盖住' };
        }
        return {
            ok: true,
            top: Math.round(rect.top),
            bottom: Math.round(rect.bottom),
            viewportH: window.innerHeight,
        };
    }, { selector, label });

    expect(info.ok, info.reason ?? '').toBe(true);
    return info as { top: number; bottom: number; viewportH: number };
}

// seedSession 用注入的 cookie + 持久化路由进入指定页面。
// 路由状态存在 zustand persist 的 nav-storage 里, 键是 currentPage(不是 activeItem)。
async function seedSession(page: Page, page_: string, section: string) {
    const jwt = process.env.SWITCHYARD_E2E_JWT;
    if (!jwt) throw new Error('缺少 SWITCHYARD_E2E_JWT 环境变量');
    await page.context().addCookies([{ name: 'auth', value: jwt, domain: '127.0.0.1', path: '/' }]);
    // 落地页与控制台是同一份 SPA: 未登录看落地页, 带 cookie 时进控制台, 没有独立的 /web/ 路径。
    await page.goto('/');
    await page.evaluate(([p, s]) => {
        localStorage.setItem('nav-storage', JSON.stringify({ state: { currentPage: p, settingSection: s }, version: 0 }));
    }, [page_, section]);
    await page.goto('/');
    await page.waitForLoadState('networkidle');
}

// openChannelCard 打开第一张渠道卡的详情弹窗, 失败即重试一次。
//
// 渠道页是懒加载模块, networkidle 之后卡片仍可能没挂上; 而点击一旦落在空窗期就静默失败,
// 弹窗永远不开, 最后表现为超时(1024x600 上偶发过)。这里显式等卡片可见再点,
// 并给一次重试兜住懒加载与动画的时序抖动。
async function openChannelCard(page: Page) {
    const card = page.getByText('budgetai.cc').first();
    const dialog = page.locator(DIALOG);
    for (let attempt = 0; attempt < 2; attempt++) {
        await card.waitFor({ state: 'visible', timeout: 25000 });
        await card.click();
        try {
            await dialog.waitFor({ state: 'visible', timeout: 8000 });
            return;
        } catch {
            if (attempt === 1) throw new Error('渠道卡点了两次都没打开详情弹窗');
            await page.waitForTimeout(500);
        }
    }
}

test.describe('渠道编辑弹窗', () => {
    // v0.39.5: SHELL_HEIGHT 上的 flex-1 让描述层塌成 0, 被外层 min-h 撑成 320px;
    // 表单按 464px 渲染, 底部按钮行被 overflow-hidden 切掉且无处可滚, 五档视口全中。
    for (const [w, h] of [[1440, 900], [1280, 720], [1024, 600], [390, 844], [360, 640]] as [number, number][]) {
        test('保存按钮可见可点 @ ' + w + 'x' + h, async ({ page }) => {
            await page.setViewportSize({ width: w, height: h });
            await seedSession(page, 'channel', 'personal');

            // 打开一张渠道卡, 再切到编辑态。
            //
            await openChannelCard(page);
            await page.evaluate((dialog) => {
                const root = document.querySelector(dialog as string);
                const btn = root?.querySelector('button[aria-label="编辑"]') as HTMLElement | null;
                btn?.click();
            }, DIALOG);
            await page.waitForTimeout(600);

            const form = page.locator(DIALOG + ' form');
            await expect(form).toBeVisible();

            const save = form.locator('button[type="submit"]');
            await expect(save).toBeVisible();

            const box = await expectClickable(page, DIALOG + ' form button[type="submit"]', '保存按钮');

            // 保存按钮必须完整落在视口内 —— 半截露在屏幕外同样点不到。
            expect(box.top).toBeGreaterThanOrEqual(0);
            expect(box.bottom).toBeLessThanOrEqual(box.viewportH);

            // 五个步骤逐个切过去, 每一步按钮都在原位站得住:
            // 有步骤内容超过固定高度时, 该由步骤区内部滚动消化, 不能让按钮被顶出去。
            //
            // 只取步骤导航里的按钮, 且切走之后先等退场动画结束: 表单与统计卡是两个绝对定位、
            // 重叠一次的视图(见 Card.tsx 的注释), 动画期间正在淡出的那份还垫在下面,
            // 命中测试会打到它身上 —— 那是转场, 不是缺陷。
            const nav = form.locator('nav > button');
            const steps = await nav.allTextContents();
            expect(steps.length).toBeGreaterThan(0);
            for (let i = 0; i < steps.length; i++) {
                const label = (steps[i] ?? '').trim();
                // 「返回统计」是退场入口, 不是步骤: 点了表单就没了, 不在本用例的覆盖范围内。
                if (label.includes('返回') || label.includes('Back')) continue;
                await nav.nth(i).click();
                await page.waitForTimeout(700);
                await expectClickable(page, DIALOG + ' form button[type="submit"]', '保存按钮(步骤 ' + label + ')');
            }
        });
    }
});

test.describe('限流管理页策略清单', () => {
    // v0.39.4: 用量面板 shrink-0 拒绝压缩, 清单 flex-1 min-h-0 让到底, 1280x720 下清单只剩 2px。
    for (const [w, h] of [[1440, 900], [1280, 720], [390, 844], [360, 640]] as [number, number][]) {
        test('策略清单有可用的高度 @ ' + w + 'x' + h, async ({ page }) => {
            await page.setViewportSize({ width: w, height: h });
            await seedSession(page, 'setting', 'ratelimit');

            // 先用界面里稳定的几何量探到清单: 策略清单是整个视图里唯一带 min-h 兜底的那块表格容器。
            // 不按表头文字匹配 —— 表头随语言变, 也不按类名匹配 —— 类名随构建变;
            // 而 min-h-[260px] 正是 v0.39.4 定下的下限, 它生效与否就是本用例要验的东西。
            const box = await page.evaluate(() => {
                const cards = [...document.querySelectorAll('div')].filter(
                    (d) => typeof d.className === 'string' && d.className.includes('overflow-auto') && d.querySelector('table'),
                );
                if (cards.length === 0) return null;
                // 取最高的那个: 用量表被 max-h 封顶(220px), 策略清单才是主张。
                const best = cards
                    .map((d) => ({ height: Math.round(d.getBoundingClientRect().height), el: d }))
                    .sort((a, b) => b.height - a.height)[0];
                return { height: best.height, candidates: cards.length };
            });

            expect(box, '没有找到策略清单容器').not.toBeNull();
            expect(box!.height).toBeGreaterThanOrEqual(260);
        });
    }
});
