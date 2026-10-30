import { beforeEach, describe, expect, it } from 'vitest';
import type { QueryClient } from '@tanstack/react-query';
import {
    webhookListQueryOptions,
    webhookLogsQueryOptions,
    WEBHOOK_LOG_PAGE_SIZE,
    WEBHOOK_PAGE_SIZE,
    type WebhookDeliveryLog,
    type WebhookView,
} from '@/api/webhook';
import { useAuthStore } from '@/api/user';
import { renderSSR, type LocaleMessages } from '@/test/ssr';
import en from '@/locales/en.json';
import zhHans from '@/locales/zh_hans.json';
import zhHant from '@/locales/zh_hant.json';
import { WebhookForm } from './WebhookForm';
import { WebhookLogs } from './WebhookLogs';
import { Webhooks } from './index';

// 页面组件的 mock SSR 冒烟(与工单同款口径): 不起服务器, 用预置 query 缓存把组件
// 渲染到各状态分支, 断言关键标记真的出现在 HTML 里。安全红线也在这里钉:
// 密钥不回显、投递日志不渲染任何响应体(HTML)。

const row: WebhookView = {
    id: 7,
    url: 'https://hooks.example.com/abc',
    events: ['ticket', 'balance_low'],
    enabled: true,
    retry_count: 3,
    has_secret: true,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-02T00:00:00Z',
    last_status: 'succeeded',
};

const logRow = {
    delivery_id: 'dlv-20260101-0001',
    event: 'ticket',
    status: 'succeeded',
    attempts: 2,
    http_status: 200,
    error: 'dial tcp: i/o timeout',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:01Z',
    // 后端将来就算带回响应体也不许渲染 —— 先把这条红线写成回归测试。
    response_body: '<b>boom</b>',
} as WebhookDeliveryLog & { response_body: string };

// SSR_USER_ID mock SSR 里组件读到的身份: zustand v5 的服务端快照取 getInitialState()
// (浏览器里 useSyncExternalStore 走 getSnapshot 实时状态, 不受影响), 所以种子必须按初始身份播种。
// 缓存隔离本身由 queryKey 里的 userId 保证, 见 api/webhook.test.ts 的「queryKey 身份隔离」。
const SSR_USER_ID = 0;

// seedList 预置订阅列表缓存; userId 必须与组件读到的身份一致, 否则走不到数据分支。
function seedList(client: QueryClient, userId: number, items: WebhookView[] = [row], total = items.length) {
    client.setQueryData(webhookListQueryOptions(userId, 1, WEBHOOK_PAGE_SIZE).queryKey, { items, total });
}

// WebhookLogs 的 userId 由页面以 prop 传入(测试里是 7), 种子按同一个身份播种。
function seedLogs(client: QueryClient, items: WebhookDeliveryLog[], id: number | null = 7) {
    client.setQueryData(webhookLogsQueryOptions(7, id, 1, WEBHOOK_LOG_PAGE_SIZE).queryKey, { items, total: items.length });
}

beforeEach(() => {
    useAuthStore.setState({ userId: 7, isAuthenticated: true, isLoading: false, isAPIKeyAuth: false, token: null, role: 'user' });
});

describe('Webhooks 列表(mock SSR)', () => {
    it('列表渲染行数据、操作入口与投递须知', () => {
        const html = renderSSR(<Webhooks />, (client) => seedList(client, SSR_USER_ID));
        expect(html).toContain('https://hooks.example.com/abc'); // 订阅地址
        expect(html).toContain('Secret configured (write-only)'); // 已设密钥只提示存在, 不回显
        expect(html).toContain('Test #7'); // 测试动作的可访问标签
        expect(html).toContain('Logs #7'); // 投递日志入口的可访问标签
        expect(html).toContain('Edit'); // 编辑入口
        expect(html).toContain('Delete'); // 删除入口
        expect(html).toContain('Ticket reply'); // 事件徽标(通知三语文案)
        expect(html).toContain('deduplicate by the delivery id'); // 至少一次投递须知
        expect(html).toContain('Only public HTTPS endpoints on port 443 are allowed.'); // 443 须知
        expect(html).toContain('DingTalk/Feishu proprietary formats are not supported.'); // 只做通用 JSON 签名
        expect(html).not.toContain('Loading...'); // 有数据就不再停在加载态
    });

    it('空列表渲染空态提示', () => {
        const html = renderSSR(<Webhooks />, (client) => seedList(client, SSR_USER_ID, [], 0));
        expect(html).toContain('No subscriptions yet');
    });

    it('未取数时停在加载态', () => {
        const html = renderSSR(<Webhooks />);
        expect(html).toContain('Loading...');
    });

    it('身份缓存隔离: 他人身份的缓存数据不参与渲染', () => {
        // 缓存里只有 userId=7 的订阅, 当前渲染身份是另一个 userId: 界面必须停在加载态,
        // 而不是把别的账号的订阅(URL)渲染出来。真实换号场景同理 —— queryKey 带身份, 互不串号。
        const html = renderSSR(<Webhooks />, (client) => seedList(client, 7));
        expect(html).toContain('Loading...');
        expect(html).not.toContain('https://hooks.example.com/abc');
    });
});

describe('Webhooks 三语(mock SSR)', () => {
    // locale 用 provider/locale.tsx 的 BCP-47 标签(zh_hans -> zh-Hans); 内部 id 不是合法语言标签,
    // 直接喂给 IntlProvider 会让带参数的 ICU 文案解析失败(纯文案看不出来)。
    const cases: [string, LocaleMessages, string, string][] = [
        ['en', en, 'Webhook Subscriptions', 'Page 1/1 - 1 total'],
        ['zh-Hans', zhHans as LocaleMessages, 'Webhook 订阅', '第 1/1 页 - 共 1 条'],
        ['zh-Hant', zhHant as LocaleMessages, 'Webhook 訂閱', '第 1/1 頁 - 共 1 條'],
    ];

    it.each(cases)('%s 渲染对应语言的标题与分页摘要(ICU 参数按语言格式化)', (_locale, messages, title, summary) => {
        const html = renderSSR(<Webhooks />, (client) => seedList(client, SSR_USER_ID), { locale: _locale, messages });
        expect(html).toContain(title);
        expect(html).toContain(summary);
    });

    it('测试动作只声称「已排队」, 不声称发送成功', () => {
        // 异步入队接口只回 queued; 文案一旦改成「发送成功」就是把没发生的事说成发生了。
        expect(en.webhook.test.toastQueued).toMatch(/queue/i);
        expect(en.webhook.test.toastQueued).not.toMatch(/sent|success/i);
        expect(zhHans.webhook.test.toastQueued).toContain('排队');
        expect(zhHans.webhook.test.toastQueued).not.toContain('成功');
        expect(zhHant.webhook.test.toastQueued).toContain('排隊');
        expect(zhHant.webhook.test.toastQueued).not.toContain('成功');
    });
});

describe('WebhookForm(mock SSR)', () => {
    it('创建表单: 可见标签、URL 输入、密钥口令框与五条须知', () => {
        const html = renderSSR(<WebhookForm mode="create" onSaved={() => {}} onCancel={() => {}} />);
        expect(html).toContain('Endpoint URL'); // 可见 label
        expect(html).toContain('type="url"'); // URL 专用输入
        expect(html).toContain('Signing secret');
        expect(html).toContain('type="password"'); // 密钥用口令框
        expect(html).toContain('new-password'); // 不给浏览器自动回填旧密钥
        expect(html).toContain('Retry count');
        expect(html).toContain('role="switch"'); // 启停开关
        expect(html.match(/role="checkbox"/g)).toHaveLength(9); // 事件多选 = 通知类型全集
        expect(html).toContain('Only public HTTPS endpoints on port 443 are allowed.');
        expect(html).toContain('The signing secret is write-only');
        expect(html).toContain('times out after 5 seconds');
        expect(html).toContain('deduplicate by the delivery id');
        expect(html).toContain('DingTalk/Feishu proprietary formats are not supported.');
        expect(html).toContain('>Create<'); // 提交按钮
    });

    it('密钥口令框永远是空值: 不回显、不注入已有值', () => {
        // 编辑已有订阅也一样: 后端只回 has_secret, 界面没有任何可回显的密钥。
        const html = renderSSR(<WebhookForm mode="edit" initial={row} onSaved={() => {}} onCancel={() => {}} />);
        expect(html).not.toMatch(/type="password"[^>]*value="[^"]+"/);
        expect(html).toContain('Leave empty to keep the current secret.'); // 编辑留空=保留
        expect(html).toContain('Secret configured (write-only)');
        expect(html).toContain('>Save<');
    });
});

describe('WebhookLogs 投递日志(mock SSR)', () => {
    it('日志渲染状态/HTTP 码/脱敏错误/attempts, 不渲染任何响应体', () => {
        const html = renderSSR(<WebhookLogs userId={7} id={7} onBack={() => {}} />, (client) => seedLogs(client, [logRow]));
        expect(html).toContain('dlv-20260101-0001'); // delivery id(接收方按它去重)
        expect(html).toContain('Ticket reply');
        expect(html).toContain('Succeeded'); // 状态(与 model.WebhookDeliveryStatus 同词)
        expect(html).toContain('200'); // HTTP 码
        expect(html).toContain('dial tcp: i/o timeout'); // 脱敏错误摘要
        expect(html).toContain('Back to list');
        // 响应体(哪怕后端将来带回 HTML)一概不渲染。
        expect(html).not.toContain('boom');
        expect(html).not.toContain('<b>');
    });

    it('空日志渲染空态提示', () => {
        const html = renderSSR(<WebhookLogs userId={7} id={7} onBack={() => {}} />, (client) => seedLogs(client, []));
        expect(html).toContain('No deliveries yet');
    });

    it('加载中也有返回列表入口', () => {
        const html = renderSSR(<WebhookLogs userId={7} id={7} onBack={() => {}} />);
        expect(html).toContain('Loading...');
        expect(html).toContain('aria-label="Back to list"');
    });

    it('无数据时同时给重试和返回列表(与真实请求报错同一条 UI)', () => {
        const html = renderSSR(<WebhookLogs userId={7} id={null} onBack={() => {}} />);
        expect(html).toContain('Failed to load');
        expect(html).toContain('Retry');
        expect(html).toContain('aria-label="Back to list"');
    });
});
