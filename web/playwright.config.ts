import { defineConfig } from '@playwright/test';

// 弹窗与面板的布局回归跑在真实浏览器里。
//
// 这一族缺陷(容器在纵轴塌陷 -> 内容被 overflow-hidden 切掉 -> 连滚动都没有)单测测不出来,
// 必须真的算一次布局。
//
// 关键: 被测页面由本仓库的源码现场编译(vite dev), 而不是打线上/已部署的产物 ——
// 打已部署产物的话, 用例验的是"上一次构建有没有问题", 改坏了源码它照样绿。
// vite dev 把 /api 反代到后端, cookie 与源同域(127.0.0.1), 认证链路与生产一致。
//
// 会话由一个登录后的 JWT 驱动: 设 SWITCHYARD_E2E_JWT 即可。取法见 web/e2e/README.md。
// 控制台布局用例缺 JWT 时直接失败；scripts/check.sh 可明确跳过该组。
const PORT = Number(process.env.SWITCHYARD_E2E_PORT || 5199);
// 后端地址: 认证与数据都来自它。
const API = process.env.SWITCHYARD_E2E_API || 'http://127.0.0.1:8088';

export default defineConfig({
    testDir: './e2e',
    testIgnore: ['public-status.spec.ts', 'public-live.spec.ts'], // standalone template tests: pnpm run test:public
    fullyParallel: false,
    forbidOnly: !!process.env.CI,
    retries: 0,
    workers: 1,
    reporter: [['list']],
    use: {
        // 直连 vite dev: 被测的就是工作区里的源码。
        baseURL: process.env.SWITCHYARD_E2E_BASE_URL || 'http://127.0.0.1:' + PORT,
        headless: true,
        launchOptions: {
            // 容器内跑 chromium 需要关沙箱, 否则起不来。
            args: ['--no-sandbox'],
        },
    },
    // 自起 dev server; 已有一个在跑就复用, 不重复占端口。
    webServer: {
        // 必须显式 --host 127.0.0.1: 默认只监听 localhost, 在容器里常解析成 ::1,
        // 而下面探测与 baseURL 用的是 127.0.0.1, 会一直等不到就绪。
        command: 'pnpm exec vite --host 127.0.0.1 --port ' + PORT + ' --strictPort',
        url: 'http://127.0.0.1:' + PORT,
        reuseExistingServer: !process.env.CI,
        timeout: 120000,
        // 只覆盖反代目标。千万不要在这里关 HMR/文件监听: 那会让 dev server 不再重新扫描源码,
        // Tailwind 的类名集合就停在启动那一刻 —— 改坏源码它照样用旧 CSS, 用例静默变绿。
        env: {
            VITE_PROXY_TARGET: API,
        },
    },
    projects: [
        {
            name: 'chromium',
            use: { browserName: 'chromium' },
        },
    ],
});
