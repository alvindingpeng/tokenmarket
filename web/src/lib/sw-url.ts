/**
 * serviceWorkerURL 生成 Service Worker 的注册地址, 并把应用版本带进查询串。
 *
 * 为什么需要它: 浏览器的更新检查只在脚本内容变化时才认为有新版本, 而 sw.js 是 public 目录里的
 * 静态文件 —— 只要缓存策略没变, 每次发版它都字节一致, 于是浏览器会一直复用旧的 Service Worker,
 * 旧 worker 里的 CACHE_VERSION 也不会变, 旧的应用壳缓存于是跨版本存活, 用户看到的是上一个版本。
 * 把版本号拼进注册地址后, 每次发版都换一个脚本 URL, 浏览器必然重新安装 worker;
 * worker 再用这个版本号做缓存名, 激活阶段即清掉上一版的缓存。
 *
 * 抽成纯函数是为了可测: main.tsx 里的副作用在 node 里跑不起来。
 */
export function serviceWorkerURL(version: string | undefined): string {
    // 空版本(本地 dev 构建)回退到固定标记, 避免拼出 sw.js?v=unknown 这类会频繁变动的地址。
    const value = version?.trim() || 'dev';
    return `./sw.js?v=${encodeURIComponent(value)}`;
}
