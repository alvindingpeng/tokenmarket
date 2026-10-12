import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Monitor, Globe, Clock, Shield, Filter, HelpCircle, SlidersHorizontal, X, RefreshCw } from 'lucide-react';
import Github from '@thesvg/react/github';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { useSettingList, useSetSetting, SettingKey } from '@/api/setting';
import { toast } from 'sonner';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';

// TOKEN_CONFIGURED 是后端对「已配置令牌」的回显占位: 真实令牌不会下发到浏览器,
// 输入框留空即保持原值; 要清空请填 - 之外的写法见输入框提示。
const TOKEN_CONFIGURED = '__configured__';

export function SettingSystem() {
    const t = useTranslations('setting');
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();

    const [proxyUrl, setProxyUrl] = useState('');
    const [statsSaveInterval, setStatsSaveInterval] = useState('');
    const [corsAllowOrigins, setCorsAllowOrigins] = useState('');
    const [corsInputValue, setCorsInputValue] = useState('');
    const [modelFilter, setModelFilter] = useState('');
    const [scoreWeights, setScoreWeights] = useState({ price: '', latency: '', success: '' });
    const [githubToken, setGithubToken] = useState('');
    const [updateCheckEnabled, setUpdateCheckEnabled] = useState(true);
    const [updateCheckMinutes, setUpdateCheckMinutes] = useState('');

    const initialProxyUrl = useRef('');
    const initialStatsSaveInterval = useRef('');
    const initialCorsAllowOrigins = useRef('');
    const initialModelFilter = useRef('');
    const initialScoreWeights = useRef({ price: '', latency: '', success: '' });
    const initialGithubToken = useRef('');
    const initialUpdateCheckEnabled = useRef('');
    const initialUpdateCheckMinutes = useRef('');

    useEffect(() => {
        if (settings) {
            const proxy = settings.find(s => s.key === SettingKey.ProxyURL);
            const interval = settings.find(s => s.key === SettingKey.StatsSaveInterval);
            const cors = settings.find(s => s.key === SettingKey.CORSAllowOrigins);
            const modelFilterSetting = settings.find(s => s.key === SettingKey.ModelFilter);
            if (proxy) {
                queueMicrotask(() => setProxyUrl(proxy.value));
                initialProxyUrl.current = proxy.value;
            }
            if (interval) {
                queueMicrotask(() => setStatsSaveInterval(interval.value));
                initialStatsSaveInterval.current = interval.value;
            }
            if (cors) {
                queueMicrotask(() => setCorsAllowOrigins(cors.value));
                initialCorsAllowOrigins.current = cors.value;
            }
            if (modelFilterSetting) {
                queueMicrotask(() => setModelFilter(modelFilterSetting.value));
                initialModelFilter.current = modelFilterSetting.value;
            }
            const price = settings.find(s => s.key === SettingKey.ScorePriceWeight);
            const latency = settings.find(s => s.key === SettingKey.ScoreLatencyWeight);
            const success = settings.find(s => s.key === SettingKey.ScoreSuccessWeight);
            const next = {
                price: price ? price.value : '',
                latency: latency ? latency.value : '',
                success: success ? success.value : '',
            };
            queueMicrotask(() => setScoreWeights(next));
            initialScoreWeights.current = next;

            // GitHub 访问令牌: 只用于抬高更新检查的 API 限流; 留空即匿名访问。
            const token = settings.find(s => s.key === SettingKey.GithubToken);
            if (token) {
                const value = token.value === TOKEN_CONFIGURED ? '' : token.value;
                queueMicrotask(() => setGithubToken(value));
                initialGithubToken.current = value;
            }

            // 更新检查开关与周期: 与令牌同区管理, 保存后热应用到调度器(无需重启)。
            const enabled = settings.find(s => s.key === SettingKey.UpdateCheckEnabled);
            if (enabled) {
                queueMicrotask(() => setUpdateCheckEnabled(enabled.value === 'true'));
                initialUpdateCheckEnabled.current = enabled.value;
            }
            const minutes = settings.find(s => s.key === SettingKey.UpdateCheckMinutes);
            if (minutes) {
                queueMicrotask(() => setUpdateCheckMinutes(minutes.value));
                initialUpdateCheckMinutes.current = minutes.value;
            }
        }
    }, [settings]);

    const handleSave = (key: string, value: string, initialValue: string) => {
        if (value === initialValue) return;

        setSetting.mutate({ key, value }, {
            onSuccess: () => {
                toast.success(t('saved'));
                if (key === SettingKey.ProxyURL) {
                    initialProxyUrl.current = value;
                } else if (key === SettingKey.StatsSaveInterval) {
                    initialStatsSaveInterval.current = value;
                } else if (key === SettingKey.CORSAllowOrigins) {
                    initialCorsAllowOrigins.current = value;
                } else if (key === SettingKey.ModelFilter) {
                    initialModelFilter.current = value;
                } else if (key === SettingKey.GithubToken) {
                    initialGithubToken.current = value;
                } else if (key === SettingKey.UpdateCheckEnabled) {
                    initialUpdateCheckEnabled.current = value;
                } else if (key === SettingKey.UpdateCheckMinutes) {
                    initialUpdateCheckMinutes.current = value;
                } else if (key === SettingKey.ScorePriceWeight) {
                    initialScoreWeights.current = { ...initialScoreWeights.current, price: value };
                } else if (key === SettingKey.ScoreLatencyWeight) {
                    initialScoreWeights.current = { ...initialScoreWeights.current, latency: value };
                } else if (key === SettingKey.ScoreSuccessWeight) {
                    initialScoreWeights.current = { ...initialScoreWeights.current, success: value };
                }
            }
        });
    };

    const saveScoreWeight = (dimension: 'price' | 'latency' | 'success', raw: string) => {
        const key = dimension === 'price' ? SettingKey.ScorePriceWeight
            : dimension === 'latency' ? SettingKey.ScoreLatencyWeight
            : SettingKey.ScoreSuccessWeight;
        const trimmed = raw.trim();
        const parsed = Number.parseInt(trimmed, 10);
        const normalized = Number.isFinite(parsed) ? String(Math.min(Math.max(parsed, 0), 100)) : '0';
        setScoreWeights(prev => ({ ...prev, [dimension]: normalized }));
        handleSave(key, normalized, initialScoreWeights.current[dimension]);
    };

    // saveUpdateCheckMinutes 归一化后落库: 后端已限制 1-1440 分钟, 这里同样夹住,
    // 免得管理员填 0 或负数把更新检查变成每秒钟打一次 GitHub API。
    const saveUpdateCheckMinutes = (raw: string) => {
        const parsed = Number.parseInt(raw.trim(), 10);
        const normalized = String(Number.isFinite(parsed) ? Math.min(Math.max(parsed, 1), 1440) : 60);
        setUpdateCheckMinutes(normalized);
        handleSave(SettingKey.UpdateCheckMinutes, normalized, initialUpdateCheckMinutes.current);
    };

    const corsAllowOriginsList = useMemo(() => {
        const value = corsAllowOrigins.trim();
        if (!value) return [];
        if (value === '*') return ['*'];
        return Array.from(new Set(
            value
                .split(/[,\n，]/)
                .map(item => item.trim())
                .filter(Boolean)
        ));
    }, [corsAllowOrigins]);

    const corsAllowOriginsDisplay = useMemo(
        () => (corsAllowOriginsList.length > 0 ? corsAllowOriginsList.join(', ') : t('corsAllowOrigins.hint')),
        [corsAllowOriginsList, t]
    );

    const saveCorsAllowOrigins = (origins: string[]) => {
        const normalizedOrigins = Array.from(new Set(
            origins
                .map(origin => origin.trim())
                .filter(Boolean)
        ));
        const normalizedValue = normalizedOrigins.includes('*') ? '*' : normalizedOrigins.join(',');
        setCorsAllowOrigins(normalizedValue);
        handleSave(SettingKey.CORSAllowOrigins, normalizedValue, initialCorsAllowOrigins.current);
    };

    const handleAddCorsOrigin = () => {
        const newOrigins = Array.from(new Set(
            corsInputValue
                .split(/[,\n，]/)
                .map(item => item.trim())
                .filter(Boolean)
        ));
        if (newOrigins.length === 0) return;

        if (newOrigins.includes('*')) {
            saveCorsAllowOrigins(['*']);
            setCorsInputValue('');
            return;
        }

        const base = corsAllowOriginsList.includes('*') ? [] : corsAllowOriginsList;
        const merged = Array.from(new Set([...base, ...newOrigins]));
        saveCorsAllowOrigins(merged);
        setCorsInputValue('');
    };

    const handleRemoveCorsOrigin = (originToRemove: string) => {
        const nextOrigins = corsAllowOriginsList.filter(origin => origin !== originToRemove);
        saveCorsAllowOrigins(nextOrigins);
    };

    return (
        <div className="rounded-3xl border border-border bg-card p-6 space-y-5">
            <h2 className="text-lg font-bold text-card-foreground flex items-center gap-2">
                <Monitor className="h-5 w-5" />
                {t('system')}
            </h2>

            {/* 代理地址 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Globe className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('proxyUrl.label')}</span>
                </div>
                <Input
                    value={proxyUrl}
                    onChange={(e) => setProxyUrl(e.target.value)}
                    onBlur={() => handleSave('proxy_url', proxyUrl, initialProxyUrl.current)}
                    placeholder={t('proxyUrl.placeholder')}
                    className="w-48 rounded-xl"
                />
            </div>

            {/* 统计保存周期 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Clock className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('statsSaveInterval.label')}</span>
                </div>
                <Input
                    type="number"
                    value={statsSaveInterval}
                    onChange={(e) => setStatsSaveInterval(e.target.value)}
                    onBlur={() => handleSave('stats_save_interval', statsSaveInterval, initialStatsSaveInterval.current)}
                    placeholder={t('statsSaveInterval.placeholder')}
                    className="w-48 rounded-xl"
                />
            </div>

            {/* 综合评分默认参数 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <SlidersHorizontal className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('scoreWeights.label')}</span>
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <HelpCircle className="size-4 text-muted-foreground cursor-help" />
                        </TooltipTrigger>
                        <TooltipContent side="top" sideOffset={10} align="center" className="max-w-64">
                            {t('scoreWeights.hint')}
                        </TooltipContent>
                    </Tooltip>
                </div>
                <div className="flex items-center gap-2">
                    <span className="text-xs text-muted-foreground">{t('scoreWeights.price')}</span>
                    <Input
                        type="number"
                        min={0}
                        max={100}
                        value={scoreWeights.price}
                        onChange={(e) => setScoreWeights(prev => ({ ...prev, price: e.target.value }))}
                        onBlur={(e) => saveScoreWeight('price', e.target.value)}
                        className="w-20 rounded-xl"
                    />
                    <span className="text-xs text-muted-foreground">{t('scoreWeights.latency')}</span>
                    <Input
                        type="number"
                        min={0}
                        max={100}
                        value={scoreWeights.latency}
                        onChange={(e) => setScoreWeights(prev => ({ ...prev, latency: e.target.value }))}
                        onBlur={(e) => saveScoreWeight('latency', e.target.value)}
                        className="w-20 rounded-xl"
                    />
                    <span className="text-xs text-muted-foreground">{t('scoreWeights.success')}</span>
                    <Input
                        type="number"
                        min={0}
                        max={100}
                        value={scoreWeights.success}
                        onChange={(e) => setScoreWeights(prev => ({ ...prev, success: e.target.value }))}
                        onBlur={(e) => saveScoreWeight('success', e.target.value)}
                        className="w-20 rounded-xl"
                    />
                </div>
            </div>

            {/* 全局模型过滤 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Filter className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('modelFilter.label')}</span>
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <HelpCircle className="size-4 text-muted-foreground cursor-help" />
                        </TooltipTrigger>
                        <TooltipContent side="top" sideOffset={10} align="center">
                            {t('modelFilter.hint')}
                        </TooltipContent>
                    </Tooltip>
                </div>
                <Input
                    value={modelFilter}
                    onChange={(e) => setModelFilter(e.target.value)}
                    onBlur={() => handleSave('model_filter', modelFilter, initialModelFilter.current)}
                    placeholder={t('modelFilter.placeholder')}
                    className="w-48 rounded-xl"
                />
            </div>

            {/* CORS 跨域白名单 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Shield className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('corsAllowOrigins.label')}</span>
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <HelpCircle className="size-4 text-muted-foreground cursor-help" />
                        </TooltipTrigger>
                        <TooltipContent side="top" sideOffset={10} align="center">
                            {t('corsAllowOrigins.hint')}
                            <br />
                            {t('corsAllowOrigins.example')}
                        </TooltipContent>
                    </Tooltip>
                </div>
                <Popover>
                    <PopoverTrigger asChild>
                        <button
                            type="button"
                            className="border-input focus-visible:border-ring focus-visible:ring-ring/50 w-48 min-h-9 rounded-xl border bg-transparent px-3 py-2 text-left text-sm shadow-xs transition-[color,box-shadow] outline-none focus-visible:ring-[3px]"
                        >
                            <span className={`block overflow-hidden text-ellipsis whitespace-nowrap ${corsAllowOriginsList.length === 0 ? 'text-muted-foreground' : ''}`}>
                                {corsAllowOriginsDisplay}
                            </span>
                        </button>
                    </PopoverTrigger>
                    <PopoverContent className="w-72 space-y-2 rounded-3xl p-3 bg-card">
                        <Input
                            value={corsInputValue}
                            onChange={(e) => setCorsInputValue(e.target.value)}
                            onKeyDown={(e) => {
                                if (e.key === 'Enter') {
                                    e.preventDefault();
                                    handleAddCorsOrigin();
                                }
                            }}
                            placeholder={t('corsAllowOrigins.example')}
                            className="h-9 rounded-xl"
                            autoFocus
                        />
                        <div className="max-h-48 space-y-1 overflow-y-auto">
                            {corsAllowOriginsList.length > 0 && (
                                corsAllowOriginsList.map((origin) => (
                                    <div key={origin} className="flex items-center justify-between gap-2 rounded-xl border border-border/60 px-2 py-1">
                                        <span className="break-all text-xs leading-5">{origin}</span>
                                        <button
                                            type="button"
                                            onClick={() => handleRemoveCorsOrigin(origin)}
                                            className="text-muted-foreground transition-colors hover:text-destructive"
                                            aria-label={`remove ${origin}`}
                                        >
                                            <X className="size-4" />
                                        </button>
                                    </div>
                                ))
                            )}
                        </div>
                    </PopoverContent>
                </Popover>
            </div>
            {/* GitHub 访问令牌 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Github variant="mono" className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('githubToken.label')}</span>
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <HelpCircle className="size-4 text-muted-foreground cursor-help" />
                        </TooltipTrigger>
                        <TooltipContent side="top" sideOffset={10} align="center" className="max-w-72">
                            {t('githubToken.hint')}
                        </TooltipContent>
                    </Tooltip>
                </div>
                <Input
                    type="password"
                    value={githubToken}
                    onChange={(e) => setGithubToken(e.target.value)}
                    onBlur={() => handleSave(SettingKey.GithubToken, githubToken, initialGithubToken.current)}
                    placeholder={t('githubToken.placeholder')}
                    autoComplete="off"
                    className="w-48 rounded-xl"
                />
            </div>

            {/* 更新检查开关: 关掉即停止向 GitHub 轮询版本; 站内版本信息仍照常显示当前版本。 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <RefreshCw className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('updateCheckEnabled.label')}</span>
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <HelpCircle className="size-4 text-muted-foreground cursor-help" />
                        </TooltipTrigger>
                        <TooltipContent side="top" sideOffset={10} align="center" className="max-w-72">
                            {t('updateCheckEnabled.hint')}
                        </TooltipContent>
                    </Tooltip>
                </div>
                <Switch
                    checked={updateCheckEnabled}
                    onCheckedChange={(checked) => {
                        setUpdateCheckEnabled(checked);
                        handleSave(SettingKey.UpdateCheckEnabled, String(checked), initialUpdateCheckEnabled.current);
                    }}
                />
            </div>

            {/* 更新检查间隔: 保存后调度器立刻按新周期重排, 不需要重启进程。 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Clock className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('updateCheckInterval.label')}</span>
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <HelpCircle className="size-4 text-muted-foreground cursor-help" />
                        </TooltipTrigger>
                        <TooltipContent side="top" sideOffset={10} align="center" className="max-w-72">
                            {t('updateCheckInterval.hint')}
                        </TooltipContent>
                    </Tooltip>
                </div>
                <Input
                    type="number"
                    min={1}
                    max={1440}
                    disabled={!updateCheckEnabled}
                    value={updateCheckMinutes}
                    onChange={(e) => setUpdateCheckMinutes(e.target.value)}
                    onBlur={(e) => saveUpdateCheckMinutes(e.target.value)}
                    placeholder={t('updateCheckInterval.placeholder')}
                    className="w-48 rounded-xl disabled:opacity-50"
                />
            </div>
        </div>
    );
}
