import { useTranslations } from 'use-intl';
import { TriangleAlert } from 'lucide-react';
import { useWalletSummary } from '@/api/wallet';
import { useBalanceAlert } from '@/api/balance-alert';
import { useAppStore } from '@/stores/app';
import { balanceLowState, formatBannerAmount } from './balance-low';

// BalanceLowBanner 余额不足的顶部常驻横幅。
//
// 为什么需要它: 站内通知与余额预警(op/balance_alert.go 触发 + NotifyBalanceLow)都已经存在,
// 但通知是"推"的 —— 用户不进通知中心就不知道余额已经危险, 而钱花光的那一刻请求就开始失败了。
// 常驻横幅是"摆在那里"的那一半。
//
// 四态规则照本仓约定: 加载中 / 失败 / 无数据 一律不渲染, 绝不把"没取到"显示成 0。
// 判定与格式化全在 balance-low.ts(组件文件只导出组件, 保住 Fast Refresh)。
//
// 只在**用户自己设了阈值**且可用余额跌到线下时出现。没有阈值时不提示 ——
// 没有客观判据就不替用户发明一条线, 理由见 balance-low.ts 的 balanceLowState 注释。
export function BalanceLowBanner() {
    const t = useTranslations('site');
    const setCurrentPage = useAppStore((state) => state.setCurrentPage);
    // 两条查询都只读, 不做任何写操作: 充值动作走钱包页, 这里只负责把人送过去。
    const { data: wallet } = useWalletSummary();
    const { data: alert } = useBalanceAlert();

    const state = balanceLowState(wallet, alert);
    // 不是 threshold 分支就整段不渲染(返回 null)。
    // 外层容器是常驻的空 div, 所以这里少一个横幅不会改变布局高度 —— 见 app-shell.tsx 的槽注释。
    if (state.kind !== 'threshold') return null;

    return (
        <div className="flex items-start gap-2 rounded-2xl border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-sm" data-testid="balance-low-banner">
            <TriangleAlert className="mt-0.5 size-4 shrink-0 text-amber-600 dark:text-amber-500" aria-hidden="true" />
            <div className="min-w-0 flex-1">
                <span className="font-medium text-amber-700 dark:text-amber-500">{t('balanceLow')}</span>
                <span className="mx-2 text-muted-foreground">·</span>
                {/* 文案只说事实: 可用多少、线在哪里。不催、不吓、不暗示后端有问题 ——
                    余额低是正常的使用状态, 不是系统故障。 */}
                <span className="text-foreground">
                    {t('balanceLowWithThreshold', {
                        available: formatBannerAmount(state.available, wallet),
                        // 阈值也用**钱包**的 pool_currency: threshold_cny 的名字是历史遗留, 它的单位
                        // 其实是账面币种(USD, 见 internal/model/balance_alert.go), 与可用余额同币种。
                        // 两个数字必须标同一个单位, 否则用户会以为阈值是另一种货币。
                        threshold: formatBannerAmount(state.threshold, wallet),
                    })}
                </span>
            </div>
            {/* 去充值直接跳钱包页: 不引入 React Router(本仓页面切换走 zustand 的 currentPage),
                也不在这里内联充值表单 —— 充值有独立页面, 弹窗式充值会多一套重复的校验。 */}
            <button
                type="button"
                className="shrink-0 rounded-lg border border-amber-500/40 bg-amber-500/10 px-2.5 py-1 text-xs font-medium text-amber-700 transition-colors hover:bg-amber-500/20 dark:text-amber-500"
                onClick={() => setCurrentPage('wallet')}
            >
                {t('topUp')}
            </button>
        </div>
    );
}
