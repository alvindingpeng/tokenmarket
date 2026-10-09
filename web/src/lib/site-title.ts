import { useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';
import { DEFAULT_SITE_NAME, siteConfigQueryOptions } from '@/api/site';

// useApplySiteTitle 把「系统信息配置 > 站点名称」写进浏览器标签页标题。
// 留空时回退默认品牌名而不是清成空串 —— 空标题在标签栏里只剩图标, 难以辨认。
export function useApplySiteTitle() {
    const { data: site } = useQuery(siteConfigQueryOptions);
    const name = site?.site_name?.trim() || DEFAULT_SITE_NAME;

    useEffect(() => {
        document.title = name;
    }, [name]);
}
