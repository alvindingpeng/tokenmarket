import type { ReactNode } from 'react';
import { renderToString } from 'react-dom/server';
import { IntlProvider } from 'use-intl';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import en from '@/locales/en.json';

// LocaleMessages 三语文案同构(en/zh_hans/zh_hant 键位由 locales.test.ts 钉死一致)。
export type LocaleMessages = typeof en;

// renderSSR 页面组件的 mock SSR: 用 react-dom/server 渲染出 HTML 字符串做断言。
// 不起 dev server、不开浏览器、不需要 jsdom —— 组件树在 node 里跑一遍,
// 配合预置好的 query 缓存就能走到「有数据」的渲染分支, 覆盖列表行/对话/按钮这些真实标记。
// options 允许换语言渲染: 三语测试用同一棵组件树断言各语言文案真的接上了。
export function renderSSR(
    node: ReactNode,
    seed?: (client: QueryClient) => void,
    options?: { locale?: string; messages?: LocaleMessages },
): string {
    const client = new QueryClient();
    seed?.(client);
    return renderToString(
        <QueryClientProvider client={client}>
            <IntlProvider
                locale={options?.locale ?? 'en'}
                messages={options?.messages ?? en}
                timeZone="Asia/Shanghai"
            >
                {node}
            </IntlProvider>
        </QueryClientProvider>,
    );
}
