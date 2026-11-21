import { useTranslations } from 'use-intl';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import type { ChannelFormState } from './state';

// 协议路径输入组: chat / responses / anthropic / 视频基路径。
//
// 单独成文件(而不是留在 Form.tsx 里)是为了让它能被直接渲染断言 ——
// Form.tsx 整张表单要挂在弹窗 Provider 上, 而弹窗正文在 SSR 里渲染不出来,
// 于是"输入框里到底有没有那个值"此前只能靠人工点浏览器验证。
//
// 视频那一项与前三级有实质区别, 必须写清楚, 否则一定会被当成完整端点填成 /v1/videos:
// 后端在基路径后面**自己**拼 /videos 与 /videos/{id}(见 internal/relay/channel.go),
// 填完整端点会拼出 /v1/videos/videos —— 请求打到不存在的路径, 而界面看起来"填得很对"。
export function FormPaths({ state, setState, idPrefix }: {
    state: ChannelFormState;
    setState: (next: ChannelFormState) => void;
    idPrefix: string;
}) {
    const t = useTranslations('channel.form');
    // 前三级沿用原有的字面量标签(协议名是专有名词, 不进 locale), 视频那一项同样。
    const fields = [
        ['openai_chat_completion_path', 'OpenAI Chat'],
        ['openai_response_path', 'OpenAI Responses'],
        ['anthropic_message_path', 'Anthropic'],
        ['openai_video_base_path', 'OpenAI Video'],
    ] as const;

    return (
        <>
            {fields.map(([field, label]) => (
                <div key={field} className="space-y-2">
                    <Label htmlFor={`${idPrefix}-${field}`}>{label}</Label>
                    <Input
                        id={`${idPrefix}-${field}`}
                        value={state[field]}
                        onChange={(e) => setState({ ...state, [field]: e.target.value })}
                        // 校验口径与前三级一致: 非空则须以 / 开头。
                        // 空串**不算**非法 —— 它的含义是"视频挂在域名根下", 与后端的归一结果一致。
                        aria-invalid={state[field] !== '' && !state[field].startsWith('/')}
                        className="rounded-xl font-mono text-sm"
                    />
                    {field === 'openai_video_base_path' && (
                        <p className="text-xs text-muted-foreground leading-relaxed">{t('openaiVideoBasePathHint')}</p>
                    )}
                </div>
            ))}
        </>
    );
}
