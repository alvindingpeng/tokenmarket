import { useTranslations } from 'use-intl';
import { Plus, Trash2 } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { IconButton } from '@/components/common/IconButton';
import type { ChannelFormState } from './state';

type EgressForm = ChannelFormState['egresses'][number];

// FormEgresses 渠道出口池的增删改。
//
// 出口决定"从哪条线路出去": 同一渠道可以配多条, 转发时按实测耗时择优, 某条不通时自动换下一条。
// 留空则沿用渠道自身的代理配置(上一步的 proxy / channel_proxy), 行为与旧版本完全一致。
// 行内直接编辑而不进编辑态: 出口字段少且都是短值, 就地改比"点开-改-确认"少两次交互。
export function FormEgresses({ state, setState }: {
    state: ChannelFormState;
    setState: (next: ChannelFormState) => void;
}) {
    const t = useTranslations('channel.form');

    const update = (index: number, patch: Partial<EgressForm>) => {
        setState({
            ...state,
            egresses: state.egresses.map((egress, i) => (i === index ? { ...egress, ...patch } : egress)),
        });
    };

    const remove = (index: number) => {
        setState({ ...state, egresses: state.egresses.filter((_, i) => i !== index) });
    };

    const add = () => {
        // 名称在渠道内唯一, 递增取一个未占用的默认名; 默认直连, 让新增的一行立刻是合法配置。
        let n = state.egresses.length + 1;
        while (state.egresses.some((e) => e.name === `egress-${n}`)) n += 1;
        setState({
            ...state,
            egresses: [...state.egresses, { name: `egress-${n}`, kind: 'direct', url: '', region: '', weight: 0, enabled: true }],
        });
    };

    return (
        <div className="flex flex-col gap-3 h-full min-h-0">
            <div className="flex items-center justify-between shrink-0">
                <span className="text-sm font-medium">{t('egresses')} ({state.egresses.length})</span>
                <IconButton onClick={add} className="size-9" tip={t('egressAdd')}>
                    <Plus className="size-4" />
                </IconButton>
            </div>

            <p className="text-xs text-muted-foreground shrink-0">{t('egressesHint')}</p>

            <div className="flex-1 min-h-0 overflow-y-auto overscroll-contain space-y-2">
                {state.egresses.length === 0 && (
                    <p className="rounded-xl border border-dashed p-4 text-xs text-muted-foreground">{t('egressEmpty')}</p>
                )}
                {state.egresses.map((egress, index) => (
                    <div key={index} className="grid gap-2 rounded-xl border bg-card p-3">
                        <div className="grid gap-2 @md/egress:grid-cols-[minmax(0,1fr)_8rem_5rem_auto_auto]">
                            <Input
                                className="h-9 rounded-xl"
                                placeholder={t('egressName')}
                                value={egress.name}
                                onChange={(event) => update(index, { name: event.target.value })}
                            />
                            <Select value={egress.kind} onValueChange={(value) => update(index, { kind: value as EgressForm['kind'] })}>
                                <SelectTrigger className="h-9 rounded-xl"><SelectValue /></SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="direct">{t('egressKindDirect')}</SelectItem>
                                    <SelectItem value="http">{t('egressKindHttp')}</SelectItem>
                                    <SelectItem value="socks5">{t('egressKindSocks5')}</SelectItem>
                                </SelectContent>
                            </Select>
                            <Input
                                className="h-9 rounded-xl"
                                type="number"
                                placeholder={t('egressWeight')}
                                value={egress.weight}
                                onChange={(event) => update(index, { weight: Number(event.target.value) || 0 })}
                            />
                            <Switch
                                checked={egress.enabled}
                                onCheckedChange={(checked) => update(index, { enabled: checked })}
                            />
                            <IconButton onClick={() => remove(index)} className="size-9" tip={t('egressRemove')}>
                                <Trash2 className="size-4" />
                            </IconButton>
                        </div>
                        {/* 直连没有代理地址, 显示输入框只会让人以为填了能生效。 */}
                        {egress.kind !== 'direct' && (
                            <Input
                                className="h-9 rounded-xl"
                                placeholder={egress.kind === 'socks5' ? 'socks5://host:port' : 'http://host:port'}
                                value={egress.url}
                                onChange={(event) => update(index, { url: event.target.value })}
                            />
                        )}
                        <div className="grid gap-2 @md/egress:grid-cols-[minmax(0,1fr)]">
                            <Input
                                className="h-9 rounded-xl"
                                placeholder={t('egressRegion')}
                                value={egress.region}
                                onChange={(event) => update(index, { region: event.target.value.toUpperCase() })}
                            />
                        </div>
                    </div>
                ))}
            </div>
        </div>
    );
}
