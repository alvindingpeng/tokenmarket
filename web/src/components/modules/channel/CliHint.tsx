import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { Check, Copy } from 'lucide-react';
import { cliImportHint } from './cliImportHint';
import type { AccountPlatform } from '@/api/channel';

/**
 * CliHint 显示「如何在你自己的电脑上取到这份凭据」。
 *
 * 定位说明: 这不是登录功能, 我们**不代用户登录**。我们把「跑哪条命令、产出在哪个文件」
 * 讲清楚, 用户在自己机器上登录一次再贴进来 —— 后端解析与刷新链路完全复用, 零协议改动。
 *
 * 为什么强调「在你自己的电脑上」: 我们的服务跑在服务器上, 浏览器里点授权后回调会打到
 * 服务器自己的 localhost, 那里没有也不该有监听者。不讲清这一点, 用户会以为这是个登录按钮。
 *
 * 只对有 CLI 引导的平台渲染。kiro/grok 返回 undefined, 这里直接不渲染任何东西 ——
 * 摆一块「暂无 CLI」的空框会被当成加载失败。
 */
export function CliHint({ platform }: { platform: AccountPlatform }) {
    const t = useTranslations('channel.account.cliHint');
    const hint = cliImportHint(platform);
    const [copied, setCopied] = useState<string | null>(null);

    if (!hint) return null;

    const copy = async (value: string, key: string) => {
        try {
            await navigator.clipboard.writeText(value);
            setCopied(key);
            // 2 秒后复位: 一直显示对勾会让人以为可以再点一次复制别的。
            window.setTimeout(() => setCopied((current) => (current === key ? null : current)), 2000);
        } catch {
            toast.error(t('copyFailed'));
        }
    };

    const CopyButton = ({ value, id }: { value: string; id: string }) => (
        <button
            type="button"
            aria-label={t('copy')}
            className="ml-1 inline-flex shrink-0 items-center rounded p-0.5 text-muted-foreground hover:text-foreground"
            onClick={() => void copy(value, id)}
        >
            {copied === id ? <Check className="size-3" /> : <Copy className="size-3" />}
        </button>
    );

    return (
        <div className="mt-1 space-y-1 rounded-lg border bg-muted/30 p-2.5 text-xs text-muted-foreground">
            <div className="font-medium text-foreground">{t('title')}</div>
            <div className="space-y-1">
                <div>
                    {/* 措辞随 commandKind 变: '/auth' 是会话内斜杠命令, 当成终端命令敲会
                        command not found; 而 'claude auth login' 敲进会话里同样没用。 */}
                    <span>
                        1. {hint.commandKind === 'prompt' ? t('runThenType') : t('runOnYourMachine')}
                    </span>
                    <span className="mx-1 inline-flex items-center rounded bg-background px-1 py-0.5 font-mono text-[11px] text-foreground">
                        {hint.command}
                        <CopyButton value={hint.command} id="cmd" />
                    </span>
                </div>
                <div>
                    <span>2. {t('openFile')}</span>
                    <span className="mx-1 inline-flex items-center rounded bg-background px-1 py-0.5 font-mono text-[11px] text-foreground">
                        {hint.path}
                        <CopyButton value={hint.path} id="path" />
                    </span>
                    <span className="block opacity-80">{t('windowsPath', { path: hint.windowsPath })}</span>
                </div>
                <div>3. {t('pasteContent')}</div>
            </div>
            <div className="pt-0.5 opacity-80">{t('secretNote')}</div>
        </div>
    );
}
