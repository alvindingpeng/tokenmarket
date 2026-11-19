import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslations } from 'use-intl';
import { Loader2, Wallet, Gauge, Tag, Users, Plus, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Badge } from '@/components/ui/badge';
import { Progress } from '@/components/ui/progress';
import {
    useTenantQuotaUsage,
    useTenantWallet,
    useTenantLedger,
    useTenantPricingPolicies,
    useTenantMembers,
    rechargeTenantWallet,
    createTenantPricingPolicy,
    deleteTenantPricingPolicy,
    addTenantMember,
    removeTenantMember,
} from '@/api/tenant';
import type { Tenant } from '@/api/tenant';
import { cn } from '@/lib/utils';

// UsageBar 配额用量条: 上限为 0 表示不限, 此时不显示百分比而是"不限"。
// 把 0 当成 0% 会让"没有限制"看起来像"额度已经用光", 方向正好相反。
function UsageBar({ label, used, limit, pct }: { label: string; used: number; limit: number; pct: number }) {
    const unlimited = limit <= 0;
    return (
        <div className="space-y-1">
            <div className="flex items-baseline justify-between text-xs">
                <span className="text-muted-foreground">{label}</span>
                <span className="font-medium tabular-nums">
                    {used.toLocaleString()}
                    {unlimited ? ' / ' + '∞' : ' / ' + limit.toLocaleString()}
                </span>
            </div>
            {!unlimited && (
                <Progress
                    value={Math.min(pct, 100)}
                    className={cn('h-1.5', pct >= 100 && '[&>div]:bg-destructive')}
                />
            )}
        </div>
    );
}

// TenantDetailPanel 租户详情: 配额用量 / 钱包 / 定价策略 / 成员。
// 数据不可用时按"缺省为空"渲染并给出说明, 不把整页打成报错 —— 部分端点可能尚未部署。
export function TenantDetailPanel({ tenant }: { tenant: Tenant }) {
    const t = useTranslations('tenant');
    const [modelName, setModelName] = useState('');
    const [markup, setMarkup] = useState('');
    const [memberId, setMemberId] = useState('');
    const [memberRole, setMemberRole] = useState('member');
    const [topupAmount, setTopupAmount] = useState('');
    const [topupNote, setTopupNote] = useState('');
    const [fundBusy, setFundBusy] = useState(false);
    const [fundError, setFundError] = useState('');

    const quotaUsage = useQuery(useTenantQuotaUsage(tenant.id));
    const ledger = useQuery(useTenantLedger(tenant.id));
    const wallet = useQuery(useTenantWallet(tenant.id));
    const pricing = useQuery(useTenantPricingPolicies(tenant.id));
    const members = useQuery(useTenantMembers(tenant.id));

    const addPolicy = async () => {
        const ratio = Number(markup);
        if (!Number.isFinite(ratio)) return;
        await createTenantPricingPolicy(tenant.id, {
            channel_id: null,
            model_name: modelName.trim() || null,
            markup_ratio: ratio,
            currency: '',
        });
        setModelName('');
        setMarkup('');
        await pricing.refetch();
    };

    // handleRecharge 充值后必须同时刷新钱包与流水: 只刷钱包会让"刚充的钱没出现在明细里"
    // 看起来像丢账, 而这是最不能让人怀疑的一类不一致。
    const handleRecharge = async () => {
        const amount = Number(topupAmount);
        setFundError('');
        if (!Number.isFinite(amount) || amount <= 0) {
            setFundError(t('amountInvalid'));
            return;
        }
        setFundBusy(true);
        try {
            await rechargeTenantWallet(tenant.id, amount, topupNote);
            setTopupAmount('');
            setTopupNote('');
            await Promise.all([wallet.refetch(), ledger.refetch()]);
        } catch (error) {
            // step-up 拒绝也要原样透出: 吞掉只会让管理员以为充值成功。
            setFundError(error instanceof Error ? error.message : String(error));
        } finally {
            setFundBusy(false);
        }
    };

    const addMember = async () => {
        const userID = Number(memberId);
        if (!Number.isFinite(userID) || userID <= 0) return;
        await addTenantMember(tenant.id, userID, memberRole);
        setMemberId('');
        await members.refetch();
    };

    return (
        <div className="space-y-6">
            {/* 配额用量 */}
            <section className="space-y-3">
                <h3 className="flex items-center gap-2 text-sm font-semibold">
                    <Gauge className="h-4 w-4" /> {t('quotaUsage')}
                </h3>
                {quotaUsage.isLoading ? (
                    <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
                ) : quotaUsage.data ? (
                    <div className="space-y-2">
                        <UsageBar
                            label={t('requests')}
                            used={quotaUsage.data.requests}
                            limit={quotaUsage.data.quota.requests_limit}
                            pct={quotaUsage.data.requests_pct}
                        />
                        <UsageBar
                            label={t('inputTokens')}
                            used={quotaUsage.data.input_tokens}
                            limit={quotaUsage.data.quota.input_tokens_limit}
                            pct={quotaUsage.data.input_tokens_pct}
                        />
                        <UsageBar
                            label={t('outputTokens')}
                            used={quotaUsage.data.output_tokens}
                            limit={quotaUsage.data.quota.output_tokens_limit}
                            pct={quotaUsage.data.output_tokens_pct}
                        />
                    </div>
                ) : (
                    <p className="text-xs text-muted-foreground">{t('unavailable')}</p>
                )}
            </section>

            {/* 钱包 */}
            <section className="space-y-2">
                <h3 className="flex items-center gap-2 text-sm font-semibold">
                    <Wallet className="h-4 w-4" /> {t('wallet')}
                </h3>
                {wallet.data ? (
                    <dl className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs">
                        <dt className="text-muted-foreground">{t('balance')}</dt>
                        <dd className="text-right font-medium tabular-nums">{wallet.data.balance.toFixed(6)}</dd>
                        <dt className="text-muted-foreground">{t('frozen')}</dt>
                        <dd className="text-right tabular-nums">{wallet.data.frozen.toFixed(6)}</dd>
                        <dt className="text-muted-foreground">{t('totalSpent')}</dt>
                        <dd className="text-right tabular-nums">{wallet.data.total_spent.toFixed(6)}</dd>
                        <dt className="text-muted-foreground">{t('totalRecharged')}</dt>
                        <dd className="text-right tabular-nums">{wallet.data.total_recharged.toFixed(6)}</dd>
                    </dl>
                ) : (
                    <p className="text-xs text-muted-foreground">{t('unavailable')}</p>
                )}
                <div className="flex items-center gap-2 pt-1">
                    <Input
                        className="h-8 w-24 text-xs"
                        placeholder={t('amountPlaceholder')}
                        value={topupAmount}
                        onChange={(e) => setTopupAmount(e.target.value)}
                    />
                    <Input
                        className="h-8 flex-1 text-xs"
                        placeholder={t('notePlaceholder')}
                        value={topupNote}
                        onChange={(e) => setTopupNote(e.target.value)}
                    />
                    <Button size="sm" variant="outline" className="h-8" onClick={handleRecharge} disabled={fundBusy}>
                        {fundBusy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Plus className="h-3.5 w-3.5" />}
                    </Button>
                </div>
                {fundError && <p className="text-xs text-destructive">{fundError}</p>}
                {(ledger.data?.items ?? []).length > 0 && (
                    <div className="space-y-1 pt-1">
                        <p className="text-xs font-medium text-muted-foreground">{t('ledger')}</p>
                        {(ledger.data?.items ?? []).slice(0, 5).map((row) => (
                            <div key={row.id} className="flex items-center gap-2 text-xs">
                                <span className="flex-1 truncate text-muted-foreground">{row.note || row.kind}</span>
                                <span className={cn('tabular-nums', row.amount < 0 ? 'text-destructive' : 'text-foreground')}>
                                    {row.amount > 0 ? '+' : ''}
                                    {row.amount.toFixed(6)}
                                </span>
                            </div>
                        ))}
                    </div>
                )}
            </section>

            {/* 定价策略 */}
            <section className="space-y-2">
                <h3 className="flex items-center gap-2 text-sm font-semibold">
                    <Tag className="h-4 w-4" /> {t('pricing')}
                </h3>
                <div className="space-y-1">
                    {(pricing.data ?? []).map((policy) => (
                        <div key={policy.id} className="flex items-center gap-2 rounded border px-2 py-1 text-xs">
                            <span className="flex-1 truncate">{policy.model_name || t('allModels')}</span>
                            <Badge variant="secondary" className="tabular-nums">
                                {policy.markup_ratio < 0 ? t('inherit') : policy.markup_ratio.toFixed(2)}
                            </Badge>
                            <button
                                type="button"
                                className="text-destructive hover:opacity-80"
                                onClick={async () => {
                                    await deleteTenantPricingPolicy(tenant.id, policy.id);
                                    await pricing.refetch();
                                }}
                                aria-label={t('remove')}
                            >
                                <Trash2 className="h-3.5 w-3.5" />
                            </button>
                        </div>
                    ))}
                    {(pricing.data ?? []).length === 0 && (
                        <p className="text-xs text-muted-foreground">{t('noPricing')}</p>
                    )}
                </div>
                <div className="flex items-center gap-2">
                    <Input
                        className="h-8 text-xs"
                        placeholder={t('modelPlaceholder')}
                        value={modelName}
                        onChange={(e) => setModelName(e.target.value)}
                    />
                    <Input
                        className="h-8 w-20 text-xs"
                        placeholder={t('markupPlaceholder')}
                        value={markup}
                        onChange={(e) => setMarkup(e.target.value)}
                    />
                    <Button size="sm" variant="outline" className="h-8" onClick={addPolicy}>
                        <Plus className="h-3.5 w-3.5" />
                    </Button>
                </div>
            </section>

            {/* 成员 */}
            <section className="space-y-2">
                <h3 className="flex items-center gap-2 text-sm font-semibold">
                    <Users className="h-4 w-4" /> {t('members')}
                </h3>
                {members.isError ? (
                    <p className="text-xs text-muted-foreground">{t('membersUnavailable')}</p>
                ) : (
                    <div className="space-y-1">
                        {(members.data ?? []).map((member) => (
                            <div key={member.user_id} className="flex items-center gap-2 rounded border px-2 py-1 text-xs">
                                <span className="flex-1 truncate">{member.username}</span>
                                <Badge variant="secondary">{member.role}</Badge>
                                <button
                                    type="button"
                                    className="text-destructive hover:opacity-80"
                                    onClick={async () => {
                                        await removeTenantMember(tenant.id, member.user_id);
                                        await members.refetch();
                                    }}
                                    aria-label={t('remove')}
                                >
                                    <Trash2 className="h-3.5 w-3.5" />
                                </button>
                            </div>
                        ))}
                        {(members.data ?? []).length === 0 && (
                            <p className="text-xs text-muted-foreground">{t('noMembers')}</p>
                        )}
                    </div>
                )}
                <div className="flex items-center gap-2">
                    <Input
                        className="h-8 w-24 text-xs"
                        placeholder={t('userIdPlaceholder')}
                        value={memberId}
                        onChange={(e) => setMemberId(e.target.value)}
                    />
                    <select
                        className="h-8 rounded border bg-background px-2 text-xs"
                        value={memberRole}
                        onChange={(e) => setMemberRole(e.target.value)}
                    >
                        <option value="member">member</option>
                        <option value="admin">admin</option>
                        <option value="owner">owner</option>
                    </select>
                    <Button size="sm" variant="outline" className="h-8" onClick={addMember}>
                        <Plus className="h-3.5 w-3.5" />
                    </Button>
                </div>
            </section>
        </div>
    );
}