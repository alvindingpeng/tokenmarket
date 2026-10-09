import { useTranslations } from 'use-intl';
import { Info, Tag, AlertTriangle, Download, Loader2 } from 'lucide-react';
import Github from '@thesvg/react/github';
import { useLatestInfo, useNowVersion, useUpdateCore } from '@/api/update';
import { Button } from '@/components/ui/button';
import { toast } from 'sonner';

const APP_VERSION = import.meta.env.VITE_APP_VERSION || 'unknown'; // 当前前端构建对应的应用版本。
// 项目仓库地址: 暂留空, 待本项目发布到自己的仓库后再填入(留空则不展示该行)。
const GITHUB_REPO = import.meta.env.VITE_GITHUB_REPO || '';
// 在线更新开关: 暂停使用; 待仓库地址就位且发行包可拉取后置为 true 恢复。
const UPDATE_ENABLED = false;

// SettingInfo 展示版本信息，并在更新后清理本项目的浏览器缓存。
export function SettingInfo() {
    const t = useTranslations('setting');
    const latestInfoQuery = useLatestInfo(UPDATE_ENABLED);
    const nowVersionQuery = useNowVersion();
    const updateCore = useUpdateCore();

    const backendNowVersion = nowVersionQuery.data || '';
    const latestVersion = UPDATE_ENABLED ? latestInfoQuery.data?.tag_name || '' : '';

    // 前端版本与后端当前版本不一致 → 浏览器缓存问题
    const isCacheMismatch = !!backendNowVersion && backendNowVersion !== APP_VERSION;
    // 最新版本与后端当前版本不一致 → 有新版本可更新(在线更新暂停时恒为假)。
    const hasNewVersion = UPDATE_ENABLED && !!latestVersion && !!backendNowVersion && latestVersion !== backendNowVersion;

    // clearCacheAndReload 清理当前应用目录的缓存和 Service Worker 注册后刷新页面。
    const clearCacheAndReload = async () => {
        const scope = new URL('./', document.baseURI); // 当前应用的 Service Worker 作用域及缓存所属目录。
        if ('caches' in window) {
            const names = await caches.keys();
            await Promise.all(names.filter((name) => name.startsWith(`octopus-${encodeURIComponent(scope.pathname)}-`)).map((name) => caches.delete(name)));
        }

        if ('serviceWorker' in navigator) {
            const registration = await navigator.serviceWorker.getRegistration(scope.href);
            if (registration && registration.scope === scope.href) await registration.unregister();
        }

        window.location.reload();
    };

    // handleForceRefresh 立即清理缓存并重新加载当前页面。
    const handleForceRefresh = () => {
        void clearCacheAndReload();
    };

    // handleUpdate 更新服务端程序，成功后清理旧前端缓存。
    const handleUpdate = () => {
        updateCore.mutate(undefined, {
            onSuccess: () => {
                toast.success(t('info.updateSuccess'));
                // 更新成功后清理缓存并刷新
                setTimeout(() => {
                    void clearCacheAndReload();
                }, 1500);
            },
            onError: () => {
                toast.error(t('info.updateFailed'));
            }
        });
    };

    return (
        <div className="rounded-3xl border border-border bg-card p-6 space-y-5">
            <h2 className="text-lg font-bold text-card-foreground flex items-center gap-2">
                <Info className="h-5 w-5" />
                {t('info.title')}
            </h2>
            {/* GitHub 仓库: 地址留空时整行不展示。 */}
            {GITHUB_REPO && (
                <div className="flex items-center justify-between gap-4">
                    <div className="flex items-center gap-3">
                        <Github variant="mono" className="h-5 w-5 text-muted-foreground" />
                        <span className="text-sm font-medium">{t('info.github')}</span>
                    </div>
                    <a
                        href={GITHUB_REPO}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="text-sm text-primary hover:underline"
                    >
                        {GITHUB_REPO.replace(/^https?:\/\/(www\.)?github\.com\//, '')}
                    </a>
                </div>
            )}
            {/* 当前版本 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Tag className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('info.currentVersion')}</span>
                </div>
                <div className="flex items-center gap-2">
                    {nowVersionQuery.isLoading ? (
                        <Loader2 className="size-4 animate-spin text-muted-foreground" />
                    ) : (
                        <code className="text-sm font-mono text-muted-foreground">
                            {backendNowVersion || t('info.unknown')}
                        </code>
                    )}
                </div>
            </div>

            {/* 最新版本: 在线更新暂停时整行不展示。 */}
            {UPDATE_ENABLED && (
                <div className="flex items-center justify-between gap-4">
                    <div className="flex items-center gap-3">
                        <Download className="h-5 w-5 text-muted-foreground" />
                        <span className="text-sm font-medium">{t('info.latestVersion')}</span>
                    </div>
                    <div className="flex items-center gap-2">
                        {latestInfoQuery.isLoading ? (
                            <Loader2 className="size-4 animate-spin text-muted-foreground" />
                        ) : (
                            <code className="text-sm font-mono text-muted-foreground">
                                {latestVersion || t('info.unknown')}
                            </code>
                        )}
                    </div>
                </div>
            )}

            {/* 浏览器缓存问题警告 */}
            {isCacheMismatch && (
                <div className="p-3 bg-destructive/10 border border-destructive/20 rounded-xl space-y-2">
                    <div className="flex items-start gap-3">
                        <AlertTriangle className="h-5 w-5 text-destructive shrink-0 mt-0.5" />
                        <div className="flex-1 space-y-1">
                            <p className="text-sm text-destructive font-medium">
                                {t('info.versionMismatch')}
                            </p>
                            <p className="text-xs text-muted-foreground">
                                {t('info.versionMismatchHint', { frontend: APP_VERSION, backend: backendNowVersion })}
                            </p>
                        </div>
                    </div>
                    <div className="flex justify-end">
                        <Button
                            variant="destructive"
                            size="sm"
                            onClick={handleForceRefresh}
                            className="rounded-xl"
                        >
                            {t('info.forceRefresh')}
                        </Button>
                    </div>
                </div>
            )}

            {/* 有新版本可更新 */}
            {hasNewVersion && (
                <div className="p-3 bg-primary/10 border border-primary/20 rounded-xl space-y-2">
                    <div className="flex items-start gap-3">
                        <Download className="h-5 w-5 text-primary shrink-0 mt-0.5" />
                        <div className="flex-1 space-y-1">
                            <p className="text-sm text-primary font-medium">
                                {t('info.newVersionAvailable')}
                            </p>
                            <p className="text-xs text-muted-foreground">
                                {t('info.newVersionAvailableHint')}
                            </p>
                        </div>
                    </div>
                    <div className="flex justify-end">
                        <Button
                            variant="default"
                            size="sm"
                            onClick={handleUpdate}
                            disabled={updateCore.isPending}
                            className="rounded-xl"
                        >
                            {updateCore.isPending ? t('info.updating') : t('info.updateNow')}
                        </Button>
                    </div>
                </div>
            )}
        </div>
    );
}
