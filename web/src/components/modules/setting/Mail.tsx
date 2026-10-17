import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { Loader2, Mail, Save, Send } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { useSetSetting, useSettingList } from '@/api/setting';
import { apiRequest } from '@/api/client';

// MAIL_FIELDS 邮件服务(SMTP)配置字段: 注册验证码与找回密码的发信通道。
const MAIL_FIELDS = [
    'smtp_host',
    'smtp_port',
    'smtp_username',
    'smtp_password',
    'smtp_from',
] as const;

// SettingMail SMTP 邮件服务配置: 留空表示未配置, 发码入口随之不可用; 密码掩码回显。
export function SettingMail() {
    const t = useTranslations('setting');
    const settings = useSettingList();
    const setSetting = useSetSetting();
    const [draft, setDraft] = useState<Record<string, string>>({});
    const [saving, setSaving] = useState(false);
    const [testTo, setTestTo] = useState('');
    const [testing, setTesting] = useState(false);

    // valueOf 未编辑时直接取已存值, 编辑后取草稿; 避免用 effect 回灌状态。
    const valueOf = (key: string) => draft[key] ?? (settings.data ?? []).find((item) => item.key === key)?.value ?? '';
    const setValue = (key: string, value: string) => setDraft((prev) => ({ ...prev, [key]: value }));

    // saveAll 逐项提交; 每一项都是独立设置键, 保存后立即生效, 不需要重启。
    const saveAll = async () => {
        setSaving(true);
        try {
            for (const key of MAIL_FIELDS) {
                if (draft[key] === undefined) continue;
                await setSetting.mutateAsync({ key, value: draft[key] });
            }
            setDraft({});
            toast.success(t('mail.saved'));
        } catch (error) {
            toast.error(t('mail.saveFailed'), { description: (error as Error).message });
        } finally {
            setSaving(false);
        }
    };

    // sendTest 发一封测试邮件验证配置可用; 收件人由管理员指定。
    const sendTest = async () => {
        setTesting(true);
        try {
            await apiRequest<string>('/api/v1/setting/smtp-test', { method: 'POST', body: { to: testTo } });
            toast.success(t('mail.testSent'));
        } catch (error) {
            toast.error(t('mail.testFailed'), { description: (error as Error).message });
        } finally {
            setTesting(false);
        }
    };

    return (
        <div className="space-y-5 rounded-3xl border border-border bg-card p-6">
            <h2 className="flex items-center gap-2 text-lg font-bold text-card-foreground">
                <Mail className="size-5" />
                {t('mail.title')}
            </h2>
            <p className="text-xs text-muted-foreground">{t('mail.desc')}</p>

            <div className="grid grid-cols-2 gap-3">
                <div className="space-y-2">
                    <Label htmlFor="mail-host">{t('mail.host')}</Label>
                    <Input id="mail-host" className="rounded-xl" placeholder="smtp.example.com" value={valueOf('smtp_host')} onChange={(event) => setValue('smtp_host', event.target.value)} />
                </div>
                <div className="space-y-2">
                    <Label htmlFor="mail-port">{t('mail.port')}</Label>
                    <Input id="mail-port" className="rounded-xl" placeholder="587" value={valueOf('smtp_port')} onChange={(event) => setValue('smtp_port', event.target.value)} />
                </div>
            </div>
            <div className="grid grid-cols-2 gap-3">
                <div className="space-y-2">
                    <Label htmlFor="mail-username">{t('mail.username')}</Label>
                    <Input id="mail-username" className="rounded-xl" value={valueOf('smtp_username')} onChange={(event) => setValue('smtp_username', event.target.value)} />
                </div>
                <div className="space-y-2">
                    <Label htmlFor="mail-password">{t('mail.password')}</Label>
                    <Input id="mail-password" type="password" className="rounded-xl" placeholder={t('mail.passwordPlaceholder')} value={valueOf('smtp_password')} onChange={(event) => setValue('smtp_password', event.target.value)} />
                </div>
            </div>
            <div className="space-y-2">
                <Label htmlFor="mail-from">{t('mail.from')}</Label>
                <Input id="mail-from" className="rounded-xl" placeholder="noreply@example.com" value={valueOf('smtp_from')} onChange={(event) => setValue('smtp_from', event.target.value)} />
            </div>

            <Button onClick={saveAll} disabled={saving} className="rounded-xl">
                {saving ? <Loader2 className="size-4 animate-spin" /> : <Save className="size-4" />}
                {t('mail.save')}
            </Button>

            <div className="space-y-2 border-t border-border pt-4">
                <Label htmlFor="mail-test-to">{t('mail.testTo')}</Label>
                <div className="flex gap-2">
                    <Input id="mail-test-to" type="email" className="rounded-xl" placeholder="you@example.com" value={testTo} onChange={(event) => setTestTo(event.target.value)} />
                    <Button variant="outline" className="shrink-0 rounded-xl" onClick={sendTest} disabled={testing || testTo.trim() === ''}>
                        {testing ? <Loader2 className="size-4 animate-spin" /> : <Send className="size-4" />}
                        {t('mail.testSend')}
                    </Button>
                </div>
            </div>
        </div>
    );
}
