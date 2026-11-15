import { useState } from 'react';
import { ExternalLink, Loader2, LogIn } from 'lucide-react';
import { toast } from 'sonner';
import { useTranslations } from 'use-intl';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
    MorphingDialog,
    MorphingDialogClose,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogDescription,
    MorphingDialogTitle,
    MorphingDialogTrigger,
} from '@/components/ui/morphing-dialog';
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
 */

const SUPPORTED: AccountPlatform[] = ['claude', 'codex', 'gemini'];

export function AccountSignIn({
    channelId,
    onSuccess,
}: {
    channelId: number;
    onSuccess?: () => void;
}) {
    const t = useTranslations('channel.account.signIn');
    const [platform, setPlatform] = useState<AccountPlatform>('claude');
    const [name, setName] = useState('');
    const [begin, setBegin] = useState<SignInBegin | null>(null);
    const [pasted, setPasted] = useState('');
    const [starting, setStarting] = useState(false);
    const [completing, setCompleting] = useState(false);

    const reset = () => {
        setBegin(null);
        setPasted('');
        setStarting(false);
        setCompleting(false);
    };

    // start 生成授权 URL 并打开新窗口。
    //
    // 用 window.open 而不是在同页跳转: 授权页会离开本站, 同页跳转等于把用户带走了;
    // 而贴 code 这一步还得回到本页面, 所以必须在同一个标签页里完成。
    const start = async () => {
        setStarting(true);
        try {
            const result = await accountSignInBegin(platform);
            setBegin(result);
            window.open(result.authorize_url, '_blank', 'noopener,noreferrer');
        } catch (error) {
            toast.error(error instanceof Error ? error.message : String(error));
        } finally {
            setStarting(false);
        }
    };

    const complete = async () => {
        if (!begin) return;
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
        } catch (error) {
            toast.error(error instanceof Error ? error.message : String(error));
        } finally {
            setCompleting(false);
        }
    };

    return (
        <MorphingDialog>
            <MorphingDialogTrigger className="inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1.5 text-sm hover:bg-accent">
                <LogIn className="h-3.5 w-3.5" />
                {t('trigger')}
            </MorphingDialogTrigger>
            <MorphingDialogContainer>
                <MorphingDialogContent className="relative w-[min(560px,92vw)] rounded-lg border bg-background p-5 shadow-lg">
                    <MorphingDialogTitle className="text-base font-semibold">
                        {t('title')}
                    </MorphingDialogTitle>
                    <MorphingDialogDescription className="mt-1 text-sm text-muted-foreground">
                        {t('description')}
                    </MorphingDialogDescription>

                    {begin === null ? (
                        <div className="mt-4 space-y-3">
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
                            <p className="rounded-md border border-amber-500/40 bg-amber-500/10 p-2.5 text-xs text-amber-900 dark:text-amber-200">
                                {t('clientNotice')}
                            </p>
                            <Button onClick={start} disabled={starting} className="w-full">
                                {starting && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />}
                                {t('start')}
                            </Button>
                        </div>
                    ) : (
                        <div className="mt-4 space-y-3">
                            <div className="rounded-md border bg-muted/40 p-3 text-sm">
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

                    <MorphingDialogClose className="absolute right-3 top-3" />
                </MorphingDialogContent>
            </MorphingDialogContainer>
        </MorphingDialog>
    );
}
