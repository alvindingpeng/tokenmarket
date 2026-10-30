import type { ReactNode } from 'react';
import { renderToString } from 'react-dom/server';
import { IntlProvider } from 'use-intl';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import en from '@/locales/en.json';

// renderSSR 页面组件的 mock SSR: 用 react-dom/server 渲染出 HTML 字符串做断言。
// 不起 dev server、不开浏览器、不需要 jsdom —— 组件树在 node 里跑一遍,
// 配合预置好的 query 缓存就能走到「有数据」的渲染分支, 覆盖列表行/对话/按钮这些真实标记。
export function renderSSR(node: ReactNode, seed?: (client: QueryClient) => void): string {
    const client = new QueryClient();
    seed?.(client);
    return renderToString(
        <QueryClientProvider client={client}>
            <IntlProvider locale="en" messages={en} timeZone="Asia/Shanghai">
                {node}
            </IntlProvider>
        </QueryClientProvider>,
    );
}
