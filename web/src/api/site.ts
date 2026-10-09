import { queryOptions, useQuery } from '@tanstack/react-query';
import { apiRequest } from './client';

/**
 * SiteConfig 站点信息: 管理后台「系统信息配置」写入的公开字段, 未登录也可读取。
 */
export type SiteConfig = {
    site_name: string; // 站点名称, 空表示管理员未设置, 由消费方回退默认标题。
    site_description: string; // 站点描述, 展示在登录页。
    site_contact: string; // 联系方式, 展示在登录页。
    announcement: string; // 公告正文; 后端已按开关过滤, 空串即不展示。
    maintenance_mode: boolean; // 维护模式: 非管理员登录被拒。
    maintenance_notice: string; // 维护提示文案。
};

export const DEFAULT_SITE_NAME = 'Octopus'; // 站点名称留空时的回退, 与原硬编码品牌一致。

export const siteConfigQueryOptions = queryOptions({
    queryKey: ['site', 'config'],
    queryFn: () => apiRequest<SiteConfig>('/api/v1/site/config'),
    // 消费方(浏览器标题、公告横幅、维护横幅)都读这一份缓存, 避免每个组件各发一次请求。
    staleTime: 60 * 1000,
});

/**
 * 读取站点信息。
 */
export function useSiteConfig() {
    return useQuery(siteConfigQueryOptions);
}
