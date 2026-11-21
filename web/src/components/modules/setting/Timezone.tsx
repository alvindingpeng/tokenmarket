import { useTranslations } from 'use-intl';
import { Globe } from 'lucide-react';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { toast } from 'sonner';
import { useAuthStore, useUpdateTimezone } from '@/api/user';
import { buildTimezoneOptions, detectBrowserTimezone, optionValueToTimezone, timezoneToOptionValue, type TimezoneOption } from './timezoneOptions';

// TimezoneSetting 账户设置里的时区偏好行(由 Account.tsx 内联使用, 不是独立的设置卡片)。
//
// 为什么时区要能选: 统计页的区间边界与小时刻度按用户时区算, 默认跟随服务器。
// 一个在 UTC 服务器上、人在东八区的运营, 看到的"今天"从北京时间 08:00 开始 ——
// 与他对"昨天/今天"的直觉差 8 小时, 对账时会对不上。
//
// 为什么只有三项而不是几百个时区: 实际会用的就是"跟随服务器"、"本机"与"已保存"三种,
// 取舍写在 timezoneOptions.ts 的 buildTimezoneOptions 注释里。
//
// 关于"选中即保存": 这一行没有确认按钮, 选完立刻发请求。
// 因此**不能**在本地留乐观值: 请求失败时界面必须退回 store 里的真值,
// 否则用户会带着"我已经改成上海了"的错误印象离开 —— 而他的统计页仍按 UTC 显示。
// 受控值直接读 store, store 只由 checkAuth 与保存成功两处写入, 失败时本来就没被改过。

// optionText 把一个选项翻成给人看的那一行字。
//
// 不做成导出函数是为了满足 Fast Refresh 的"组件文件只导出组件"约定;
// 它只在这里用到, 留在文件内也不会被别处误用。
function optionText(
    t: ReturnType<typeof useTranslations<'setting'>>,
    option: TimezoneOption,
): string {
    switch (option.label) {
        case 'server':
            return t('account.timezone.server');
        case 'browser':
            return t('account.timezone.browser', { tz: option.timezone ?? '' });
        case 'custom':
            return t('account.timezone.custom', { tz: option.timezone ?? '' });
    }
}

export function TimezoneSetting() {
    const t = useTranslations('setting');
    const updateTimezone = useUpdateTimezone();
    const saved = useAuthStore((state) => state.timezone);
    // 每次渲染现算: 探测很便宜(读一次 Intl 缓存), 而缓存它会让"另一台设备改了时区后本机选择"变成陈旧值。
    //
    // 探测值是否可用由 buildTimezoneOptions 负责(它会拿 Intl 再验一次), 见那里的注释。
    const browser = detectBrowserTimezone();
    const options = buildTimezoneOptions({ saved, browser });
    // 当前项: 受控 Select 的 value 与列表里的某一项必然对得上(见 buildTimezoneOptions 的覆盖判据)。
    const currentValue = timezoneToOptionValue(saved);
    const current = options.find((option) => option.value === currentValue);

    const handleChange = (optionValue: string) => {
        // 下拉里的 '@server' 必须先映射回空串: 把哨兵发出去会被后端判为非法时区名。
        updateTimezone.mutate({ timezone: optionValueToTimezone(optionValue) }, {
            onSuccess: () => toast.success(t('account.timezone.saved')),
            onError: () => toast.error(t('account.timezone.failed')),
        });
    };

    return (
        <div className="flex items-center justify-between gap-4">
            <div className="flex items-center gap-3">
                <Globe className="h-5 w-5 shrink-0 text-muted-foreground" />
                <div className="flex flex-col">
                    <span className="text-sm font-medium">{t('account.timezone.label')}</span>
                    <span className="text-xs text-muted-foreground">{t('account.timezone.hint')}</span>
                </div>
            </div>
            {/* value 走哨兵而非空串: Radix 的 SelectValue 对空串只渲染 placeholder(见 SERVER_OPTION_VALUE 注释),
                直接用 saved 会让默认态的格子里一片空白, 而"跟随服务器"是用户真正选中项, 不是未选择。 */}
            <Select value={timezoneToOptionValue(saved)} onValueChange={handleChange} disabled={updateTimezone.isPending}>
                <SelectTrigger id="account-timezone" aria-label={t('account.timezone.label')} className="w-[min(220px,45vw)] rounded-xl">
                    {/* 显式传 children 而不是留空: 留空时 Radix 靠"选中项的文字 portal 回触发器的
                        valueNode"来填这一格, 而那段克隆只在弹层被打开过之后才成立 ——
                        首屏(以及 SSR)拿到的是一格空白。显式 children 让文案由自己渲染,
                        浏览器与 SSR 看到的是同一份东西, 这一处也才有可能被自动化断言钉住。 */}
                    <SelectValue>{current ? optionText(t, current) : ''}</SelectValue>
                </SelectTrigger>
                <SelectContent>
                    {options.map((option) => (
                        // key 用 value: 哨兵与 IANA 名都非空且唯一(见 buildTimezoneOptions), 天然稳定。
                        <SelectItem key={option.value} value={option.value}>
                            {optionText(t, option)}
                        </SelectItem>
                    ))}
                </SelectContent>
            </Select>
        </div>
    );
}
