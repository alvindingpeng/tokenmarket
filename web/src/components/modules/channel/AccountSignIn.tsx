import { useState } from 'react';
import { ChevronDown, ExternalLink, Loader2, LogIn } from 'lucide-react';
import { toast } from 'sonner';
import { useTranslations } from 'use-intl';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { accountSignInBegin, accountSignInComplete, type SignInBegin } from '@/api/account-signin';
import type { AccountPlatform } from '@/api/channel';

/**
 * AccountSignIn 网页 OAuth 登录导入账号。
 *
 * 与「粘贴凭据 JSON」是并列的两种入口, 不是替代: 有的人手上已经有导出的凭据文件,
 * 有的人什么都没有只想点一下授权 —— 两种都支持才不会有一半用户被挡在门外。
 *
 * 流程三步: 打开授权页 -> 用户授权 -> 把授权完成后的 code/地址贴回来。
 * 第三步之所以要用户动手, 是因为服务端收不到浏览器回调(服务在服务器, 浏览器在用户电脑),
 * 这不是实现上的偷懒, 而是这个部署形态下唯一可行的路径。
 *
 * **刻意做成内联展开面板, 不是第二个对话框。**
 * 它被嵌在「导入账号」弹层里使用, 而那个弹层的关闭判定是 document 级的点击外部检测,
 * 且其内容通过 portal 渲染到 body —— 于是这个面板里的每一次点击都在「外部」, 会把外层
 * 弹层直接关掉, 表现为"刚点开就整窗消失"。同一页上再叠一层 portal 对话框无法避免这件事,
 * 除非给每层都加上互斥的豁免逻辑; 那比一个面板复杂得多, 也更容易在下次改弹层时重新坏掉。
 */

const SUPPORTED: AccountPlatform[] = ['claude', 'codex', 'gemini'];

export function AccountSignIn({
    channelId,
    onSuccess,
}: {
    // 可为 undefined: 独立导入模式下用户还没选目标渠道时, 入口也要可见 ——
    // 否则他根本不知道有这条路。此时展开面板只提示先选渠道, 不生成授权 URL。
    channelId?: number;
    onSuccess?: () => void;
}) {
    const t = useTranslations('channel.account.signIn');
    const [expanded, setExpanded] = useState(false);
    const [platform, setPlatform] = useState<AccountPlatform>('claude');
    const [name, setName] = useState('');
    const [begin, setBegin] = useState<SignInBegin | null>(null);
    const [pasted, setPasted] = useState('');
    const [starting, setStarting] = useState(false);
    const [completing, setCompleting] = useState(false);
    // 生成授权页失败时的原因。后端仍可能在用户把自己的 client 清空时报错,
    // 这条错误必须如实显示在面板里 —— 只弹一下 toast 会被滚走/错过,
    // 而用户看到的就是"点了没反应", 于是又跑去问为什么打不开。
    const [startError, setStartError] = useState('');

    const reset = () => {
        setBegin(null);
        setPasted('');
        setStarting(false);
        setCompleting(false);
        setStartError('');
    };

    // start 生成授权 URL 并打开新窗口。
    //
    // 用 window.open 而不是同页跳转: 授权页会离开本站, 同页跳转等于把用户带走了;
    // 而贴 code 这一步还得回到本页面, 所以必须在同一个标签页里完成。
    const start = async () => {
        setStarting(true);
        setStartError('');
        try {
            const result = await accountSignInBegin(platform);
            setBegin(result);
            window.open(result.authorize_url, '_blank', 'noopener,noreferrer');
        } catch (error) {
            const msg = error instanceof Error ? error.message : String(error);
            setStartError(msg);
            toast.error(msg);
        } finally {
            setStarting(false);
        }
    };

    const complete = async () => {
        if (!begin || channelId === undefined) return;
        const accountName = name.trim() || `${platform}-${new Date().toISOString().slice(0, 10)}`;
        setCompleting(true);
        try {
            const result = await accountSignInComplete({
                platform,
                state: begin.state,
                pasted,
                channel_id: channelId,
                name: accountName,
            });
            // 导入成功但首刷失败是「部分成功」: 账号已落库, 要如实告知而不是报错。
            // 当成错误会让用户重复登录, 然后撞上「凭据名已存在」。
            if (result.warning) {
                toast.warning(result.warning);
            } else {
                toast.success(t('success', { name: accountName }));
            }
            onSuccess?.();
            reset();
            setExpanded(false);
        } catch (error) {
            toast.error(error instanceof Error ? error.message : String(error));
        } finally {
            setCompleting(false);
        }
    };

    return (
        <div className="rounded-lg border bg-muted/20">
            <button
                type="button"
                onClick={() => {
                    setExpanded(!expanded);
                    if (expanded) reset();
                }}
                className="flex w-full items-center gap-1.5 px-3 py-2 text-sm hover:bg-accent/50"
            >
                <LogIn className="h-3.5 w-3.5" />
                {t('trigger')}
                <ChevronDown
                    className={`ml-auto h-3.5 w-3.5 transition-transform ${expanded ? 'rotate-180' : ''}`}
                />
            </button>

            {expanded && (
                <div className="border-t p-3">
                    {channelId === undefined ? (
                        // 未选目标渠道: 只提示, 不生成授权 URL —— 授权后再报"渠道不存在"
                        // 会让用户白跑一趟授权。
                        <p className="text-sm text-muted-foreground">{t('needChannel')}</p>
                    ) : begin === null ? (
                        <div className="space-y-3">
                            <div className="space-y-1.5">
                                <Label>{t('platform')}</Label>
                                <div className="flex flex-wrap gap-2">
                                    {SUPPORTED.map((p) => (
                                        <Button
                                            key={p}
                                            size="sm"
                                            variant={platform === p ? 'default' : 'outline'}
                                            onClick={() => setPlatform(p)}
                                        >
                                            {p}
                                        </Button>
                                    ))}
                                </div>
                            </div>
                            <div className="space-y-1.5">
                                <Label>{t('name')}</Label>
                                <Input
                                    value={name}
                                    onChange={(e) => setName(e.target.value)}
                                    placeholder={t('namePlaceholder')}
                                />
                            </div>
                            {/* 合规说明必须在点授权之前可见, 而不是藏在文档里:
                                授权页上显示的是上游 CLI 在申请权限, 用户有权在授权前就知道。 */}
                            {platform === 'gemini' ? (
                                <p className="rounded-md border border-blue-500/40 bg-blue-500/10 p-2.5 text-xs text-blue-900 dark:text-blue-200">
                                    {t('geminiNotice')}
                                </p>
                            ) : (
                                <p className="rounded-md border border-amber-500/40 bg-amber-500/10 p-2.5 text-xs text-amber-900 dark:text-amber-200">
                                    {t('clientNotice')}
                                </p>
                            )}
                            {startError && (
                                <p className="rounded-md border border-destructive/40 bg-destructive/10 p-2.5 text-xs text-destructive dark:text-destructive-foreground">
                                    {startError}
                                </p>
                            )}
                            <Button onClick={start} disabled={starting} className="w-full">
                                {starting && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />}
                                {t('start')}
                            </Button>
                        </div>
                    ) : (
                        <div className="space-y-3">
                            <div className="rounded-md border bg-background p-3 text-sm">
                                <div className="font-medium">{t('step1')}</div>
                                <a
                                    href={begin.authorize_url}
                                    target="_blank"
                                    rel="noopener noreferrer"
                                    className="mt-1 inline-flex items-center gap-1 break-all text-primary hover:underline"
                                >
                                    <ExternalLink className="h-3.5 w-3.5 shrink-0" />
                                    {t('openAgain')}
                                </a>
                            </div>
                            <div className="space-y-1.5">
                                <Label>{t('step2')}</Label>
                                <p className="text-xs text-muted-foreground">{begin.user_code_hint}</p>
                                <Input
                                    value={pasted}
                                    onChange={(e) => setPasted(e.target.value)}
                                    placeholder={t('pastePlaceholder')}
                                />
                            </div>
                            <Button
                                onClick={complete}
                                disabled={completing || !pasted.trim()}
                                className="w-full"
                            >
                                {completing && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />}
                                {t('complete')}
                            </Button>
                        </div>
                    )}
                </div>
            )}
        </div>
    );
}
