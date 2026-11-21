import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { Loader2, RefreshCw, Upload } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import {
    MorphingDialog,
    MorphingDialogClose,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogDescription,
    MorphingDialogTitle,
    MorphingDialogTrigger,
} from '@/components/ui/morphing-dialog';
import {
    accountImport,
    accountRefreshKey,
    type AccountImportResult,
    type AccountPlatform,
    type Dialect,
} from '@/api/channel';
import { CliHint } from './CliHint';
import { AccountSignIn } from './AccountSignIn';
import { ACCOUNT_PLATFORMS, accountPlatformMismatch, defaultAccountPlatform } from './accountPlatform';

/**
 * AccountImport 导入上游账号 JSON 作为渠道凭据。
 *
 * 只做导入与「导入后立刻验证」两件事, 不碰模型授权: 账号进来之后怎么分配模型权限, 是凭据页的活。
 * 把两件事混在一个弹层里, 用户会以为「导入」同时决定了「能对哪些模型转发」, 而实际不会。
 *
 * 平台列表写死在前端而不是从后端拉: 支持哪些平台由代码里的解析分支决定,
 * 后端没有「列出我支持哪些平台」的接口, 硬造一个只会多一份需要同步的清单。
 */


// validateAccountJSON 本地预检账号 JSON。
//
// 与后端 model.AccountJSONParse 的解析分支保持一致: 同一份 JSON
// 在这边能发现的缺字段, 就不必等后端返回 400 再告诉用户。
// 返回空数组表示通过; 否则返回缺失/错误字段的文案列表。
/** AccountFields 是账号 JSON 的宽松形状: 只做字段存在性检查, 不约束具体类型。 */
type AccountFields = Record<string, unknown>;

export function validateAccountJSON(platform: AccountPlatform, raw: string): string[] {
    const errors: string[] = [];
    if (!raw.trim()) {
        errors.push('JSON 不能为空');
        return errors;
    }
    let fields: AccountFields;
    try {
        fields = JSON.parse(raw);
    } catch {
        errors.push('不是合法的 JSON 对象');
        return errors;
    }
    // 兼容 Codex CLI 导出的外层 tokens 封装
    if (fields.tokens && typeof fields.tokens === 'object') {
        fields = fields.tokens as AccountFields;
    }

    switch (platform) {
        case 'codex': {
            if (!fields.access_token) errors.push('缺少 access_token (CLI 导出应包含 tokens 字段)');
            if (!fields.refresh_token) errors.push('缺少 refresh_token, 该账号无法自动续期');
            break;
        }
        case 'claude': {
            const nested = (fields.claudeAiOauth ?? fields) as AccountFields;
            if (!nested.accessToken && !nested.access_token) errors.push('缺少 accessToken (Claude Code 凭据文件应为 claudeAiOauth.accessToken)');
            if (!nested.refreshToken && !nested.refresh_token) errors.push('缺少 refreshToken, 该账号无法自动续期');
            break;
        }
        case 'kiro': {
            if (!fields.refreshToken) errors.push('缺少 refreshToken');
            if (!fields.clientId || !fields.clientSecret) errors.push('缺少 clientId/clientSecret');
            break;
        }
        case 'gemini': {
            // 两种形状: Gemini CLI 的 OAuth 令牌, 或 Web 端的 Cookie 会话。
            // 这里的判断必须与后端 AccountJSONParse 的 gemini 分支一致 —— 前端说"不通过"
            // 会让使用者根本提交不了, 于是后端刚支持的能力在界面上完全用不上。
            if (fields.refresh_token) {
                if (!fields.access_token) errors.push('有 refresh_token 但缺少 access_token');
                break;
            }
            if (!fields.c_ses) errors.push('缺少 c_ses, 或改用 Gemini CLI 的 oauth_creds.json(含 refresh_token)');
            break;
        }
        case 'grok': {
            if (!fields.sso) errors.push('缺少 sso');
            break;
        }
        case 'antigravity': {
            // 与后端 AccountJSONParse 的 antigravity 分支逐条对齐(internal/model/account_json.go)。
            // 这个平台只有 OAuth 一种形态: Cookie 形态上游不认, 后端会直接报错, 不必等到那一轮往返。
            // 反过来, 有 refresh_token 却没有 access_token 时后端也拒绝 —— 因为续期是定时任务的活,
            // 而首刷可能排队, 其间转发要用的正是这个 access_token, 缺了就是每请求必失败。
            if (!fields.refresh_token) {
                errors.push('缺少 refresh_token: antigravity 只接受带 refresh_token 的 OAuth 凭据(来自 Antigravity 授权流程), 不支持 Cookie 形态');
                break;
            }
            if (!fields.access_token) errors.push('有 refresh_token 但缺少 access_token');
            break;
        }
    }
    return errors;
}

export function AccountImport({ channelId, dialect, onImported, channels }: {
    // channelId 有值时是"渠道内导入": 目标固定, 不再让使用者选。
    // 无值时是"独立导入": 从渠道列表页或其它入口进来, 此时必须让他选目标渠道 ——
    // 凭据在数据模型上必须属于某个渠道(ChannelKey.ChannelID 非空), 所以"先选渠道"
    // 是绕不开的, 但至少不必先钻进某个渠道的编辑表单再回来。
    channelId?: number;
    // dialect 是该渠道的方言, 只在渠道内导入模式下给得出(独立导入的目标来自渠道列表,
    // 那边没有方言字段)。它用来做平台↔方言的前置校验, 见 accountPlatformMismatch。
    dialect?: Dialect;
    onImported?: () => void;
    // channels 只在独立导入模式下需要: 列出可选的目标渠道。
    channels?: { id: number; name: string }[];
}) {
    const t = useTranslations('channel.account');
    // 订阅/gemini_cli 渠道只有一个合法平台, 打开时就预选它 —— 见 defaultAccountPlatform 的说明。
    // 用惰性初值而不是 effect: 方言在弹层生命周期内不变(它来自已保存的渠道), 不需要跟着改。
    const [platform, setPlatform] = useState<AccountPlatform>(() => defaultAccountPlatform(dialect));
    const [name, setName] = useState('');
    const [raw, setRaw] = useState('');
    const [busy, setBusy] = useState(false);
    const [result, setResult] = useState<AccountImportResult | null>(null);
    // 独立导入模式下的目标渠道。默认取第一个: 只有一个渠道时省掉一次点击, 多个时也要让人看到这一栏。
    const [targetChannel, setTargetChannel] = useState<number | undefined>(channelId ?? channels?.[0]?.id);
    // 本地预检结果: 用户在 Textarea 里贴 JSON 时即时跑, 不等后端。
    // 这是第 6 项: 避免一轮 30 秒往返才发现 "缺 refresh_token"。
    const [validateErrors, setValidateErrors] = useState<string[]>([]);
    // 平台↔方言与平台↔字段的两类问题各算各的: 前者是"这个平台进不了这个渠道",
    // 后者是"这份 JSON 缺字段"。合成一个数组会让使用者分不清该改哪一边。
    const platformMismatch = accountPlatformMismatch(dialect, platform);
    // 校验是 (平台, JSON) 的函数, 平台一换旧结论就作废 —— 在平台按钮上重跑一遍,
    // 否则打了 codex 的平台再看 claude 的 JSON, 屏幕上留着上一轮的红字。
    const revalidate = (next: AccountPlatform, nextRaw: string) => {
        setValidateErrors(validateAccountJSON(next, nextRaw));
    };

    const submit = async () => {
        if (!name.trim()) {
            toast.error(t('nameRequired'));
            return;
        }
        if (!raw.trim()) {
            toast.error(t('rawRequired'));
            return;
        }
        // 平台与方言对不上时挡住提交并说清原因。不静默阻止: 按钮可用, 点下去给出的是
        // "为什么不能这么导"而不是"点了没反应", 后者会让人以为界面坏了。
        if (platformMismatch) {
            toast.error(t('platformNotAllowed', {
                required: platformMismatch.required,
                selected: platformMismatch.selected,
            }));
            return;
        }
        setBusy(true);
        try {
            // 目标渠道在两种模式下都必须是确定的: 拿不到就无法提交, 早在这里挡住比让后端返回一个
        // 看不出原因的错误要好。
        const target = channelId ?? targetChannel;
        if (target === undefined) {
            toast.error(t('channelRequired'));
            return;
        }
        const imported = await accountImport({ channel_id: target, platform, name: name.trim(), raw });
            setResult(imported);
            // 带 warning 表示已导入但首次刷新失败: 用警告色而不是失败色, 因为数据确实落库了。
            if (imported.warning) toast.warning(imported.warning);
            else toast.success(t('imported'));
            onImported?.();
        } catch (error) {
            toast.error((error as Error).message);
        } finally {
            setBusy(false);
        }
    };

    return (
        <MorphingDialog>
            <MorphingDialogTrigger className="inline-flex">
                <Button size="sm" variant="outline" className="rounded-xl">
                    <Upload className="size-3.5" />
                    {t('open')}
                </Button>
            </MorphingDialogTrigger>
            <MorphingDialogContainer>
                <MorphingDialogContent className="relative w-full max-w-lg rounded-2xl border bg-card p-6 shadow-lg">
                    <MorphingDialogTitle className="text-lg font-semibold">{t('title')}</MorphingDialogTitle>
                    <MorphingDialogDescription className="mt-1 text-sm text-muted-foreground">
                        {t('hint')}
                    </MorphingDialogDescription>

                    {/* 网页登录是「粘贴 JSON」之外的另一条入口, 不是替代:
                        手上已有凭据文件的人继续贴 JSON, 什么都没有的人点授权即可。
                        目标渠道沿用下面选定的那个, 两处不能各选一次。 */}
                    {/* 入口**始终**显示, 不藏在"选好渠道之后" —— 藏起来的话用户根本不知道
                        还有这条路, 只会看到粘贴框然后放弃。未选渠道时由面板自己提示先选。 */}
                    <div className="mt-3">
                        <AccountSignIn
                            channelId={channelId ?? targetChannel}
                            // 方言只在渠道内导入时已知(独立导入的目标来自渠道列表, 没有方言字段),
                            // 与粘贴 JSON 那条路径共用同一套前置校验, 见 accountPlatform。
                            dialect={channelId === undefined ? undefined : dialect}
                            onSuccess={() => setResult(null)}
                        />
                    </div>

                    <div className="mt-4 space-y-3">
                        {/* 目标渠道只在独立导入模式下出现: 渠道内导入时目标已经确定, 再摆一个不可改的
                            下拉只会让人以为可以改。
                            这条路径**做不了**平台↔方言的前置校验, 而且不是漏了: 这里的候选来自
                            渠道列表(后端 model.ChannelStats), 那份 DTO 里没有 dialect / sub_platform。
                            为了一个下拉去拉整份渠道详情也不划算 —— 详情带凭据明文, 列表页不该碰它。
                            于是这里保持宽松, 由 rawHint.antigravity 之类的说明文字承担指路责任;
                            真正的硬校验在渠道内导入路径上(此时 dialect 是已知的)。 */}
                        {channelId === undefined && (
                            <div className="space-y-1.5">
                                <Label>{t('targetChannel')}</Label>
                                <Select
                                    value={targetChannel === undefined ? '' : String(targetChannel)}
                                    onValueChange={(value) => setTargetChannel(Number(value))}
                                    disabled={!channels || channels.length === 0}
                                >
                                    <SelectTrigger className="w-full">
                                        <SelectValue placeholder={t('targetChannelPlaceholder')} />
                                    </SelectTrigger>
                                    <SelectContent>
                                        {(channels ?? []).map((item) => (
                                            <SelectItem key={item.id} value={String(item.id)}>{item.name}</SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                                {(!channels || channels.length === 0) && (
                                    <p className="text-xs text-muted-foreground">{t('targetChannelEmpty')}</p>
                                )}
                            </div>
                        )}
                        <div className="space-y-1.5">
                            <Label>{t('platform')}</Label>
                            <div className="flex flex-wrap gap-2">
                                {ACCOUNT_PLATFORMS.map((item) => (
                                    <Button
                                        key={item}
                                        type="button"
                                        size="sm"
                                        variant={platform === item ? 'default' : 'outline'}
                                        className="rounded-lg"
                                        onClick={() => {
                                            setPlatform(item);
                                            revalidate(item, raw);
                                        }}
                                    >
                                        {item}
                                    </Button>
                                ))}
                            </div>
                            {/* 平台与渠道方言对不上时当场说清, 而且说**为什么**不是通用措辞:
                                这条错误的后果在转发期才发作(每次请求 403 并熔断 30 分钟),
                                只说"不允许"会让人换个渠道继续贴, 问题原样复现。 */}
                            {platformMismatch && (
                                <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive">
                                    {t('platformNotAllowed', {
                                        required: platformMismatch.required,
                                        selected: platformMismatch.selected,
                                    })}
                                </div>
                            )}
                            {/* 选了有官方 CLI 的平台时, 告诉使用者去哪跑命令、文件在哪。
                                无 CLI 引导的平台(kiro/gemini/grok/antigravity)不渲染任何东西。 */}
                            <CliHint platform={platform} />
                        </div>
                        <div className="space-y-1.5">
                            <Label htmlFor="account-name">{t('name')}</Label>
                            <Input
                                id="account-name"
                                value={name}
                                onChange={(event) => setName(event.target.value)}
                                placeholder={t('namePlaceholder')}
                            />
                        </div>
                        <div className="space-y-1.5">
                            <Label htmlFor="account-raw">{t('raw')}</Label>
                            {/* 失焦/变化时即时预检: 用户贴上 JSON 就能看到缺什么字段, 不必等后端。 */}
                            <Textarea
                                id="account-raw"
                                value={raw}
                                onChange={(event) => {
                                    const v = event.target.value;
                                    setRaw(v);
                                    revalidate(platform, v);
                                }}
                                placeholder={t('rawPlaceholder')}
                                rows={8}
                                className="font-mono text-xs"
                            />
                            <p className="text-xs text-muted-foreground">{t(`rawHint.${platform}`)}</p>
                            {validateErrors.length > 0 && (
                                <div className="mt-1 space-y-0.5">
                                    {validateErrors.map((err, i) => (
                                        <div key={i} className="text-xs text-destructive">
                                            <span className="font-mono">✗</span> {err}
                                        </div>
                                    ))}
                                </div>
                            )}
                        </div>
                    </div>

                    {result && (
                        <div className="mt-4 space-y-1 rounded-xl border bg-muted/40 p-3 text-xs">
                            <div className="font-medium">{t('resultTitle')}</div>
                            <div>{t('resultKind', { kind: result.kind })}</div>
                            {result.email && <div>{t('resultEmail', { email: result.email })}</div>}
                            {result.expires_at && <div>{t('resultExpires', { at: result.expires_at })}</div>}
                            {!result.auto_renewable && (
                                <div className="text-amber-600 dark:text-amber-500">{t('cookieWarning')}</div>
                            )}
                            {result.refreshed_now && <div>{t('resultRefreshed')}</div>}
                        </div>
                    )}

                    <div className="mt-5 flex items-center justify-end gap-2">
                        <MorphingDialogClose className="static rounded-xl px-3 py-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground">
                            {t('cancel')}
                        </MorphingDialogClose>
                        {result && (
                            <Button
                                type="button"
                                variant="outline"
                                onClick={async () => {
                                    try {
                                        await accountRefreshKey(result.channel_key_id);
                                        toast.success(t('refreshed'));
                                    } catch (error) {
                                        toast.error((error as Error).message);
                                    }
                                }}
                            >
                                <RefreshCw className="size-3.5" />
                                {t('refreshNow')}
                            </Button>
                        )}
                        {/* 平台与方言对不上时按钮**不**置灰: 点下去弹出带原因的错误,
                            比一个点不动的按钮好排查 —— 后者会让人以为是界面坏了或权限不够。 */}
                        <Button type="button" onClick={submit} disabled={busy}>
                            {busy && <Loader2 className="size-3.5 animate-spin" />}
                            {t('submit')}
                        </Button>
                    </div>
                </MorphingDialogContent>
            </MorphingDialogContainer>
        </MorphingDialog>
    );
}
