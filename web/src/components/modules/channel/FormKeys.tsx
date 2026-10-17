import { useState } from 'react';
import { Plus, Trash2, Pencil, Check, RefreshCw } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { IconButton } from '@/components/common/IconButton';
import { useModelProbe } from './probe';
import { grantKey, type ChannelFormState } from './state';

// maskKey 显示凭据尾部, 其余以点替代。
function maskKey(key: string) {
    return key.length <= 8 ? key : `${key.slice(0, 3)}••••${key.slice(-4)}`;
}

// FormKeys 渠道凭据的增删改。
// 凭据改名后其授权要跟着改名, 否则授权会指向已不存在的凭据; 删除凭据同时删掉它的全部授权。
export function FormKeys({ state, setState }: {
    state: ChannelFormState;
    setState: (next: ChannelFormState) => void;
}) {
    const t = useTranslations('channel.form');
    const { probe, pendingKey } = useModelProbe();
    // editing 用凭据名而不是下标定位: 名字是渠道内唯一标识, 删除其他行时不会错位到别的凭据上。
    const [editing, setEditing] = useState<string | null>(null);
    const [draft, setDraft] = useState({ name: '', key: '' });

    const renameGrants = (from: string, to: string) => {
        const grants = new Map(state.grants);
        for (const modelName of state.models) {
            const old = grantKey(modelName, from);
            const grant = grants.get(old);
            if (grant === undefined) continue;
            grants.delete(old);
            grants.set(grantKey(modelName, to), grant);
        }
        return grants;
    };

    const commit = (current: string) => {
        const name = draft.name.trim();
        if (!name || state.keys.some((k) => k.name === name && k.name !== current)) return;
        setState({
            ...state,
            keys: state.keys.map((k) => (k.name === current ? { ...k, name, key: draft.key.trim() } : k)),
            grants: name === current ? state.grants : renameGrants(current, name),
        });
        setEditing(null);
    };

    const remove = (name: string) => {
        const grants = new Map(state.grants);
        for (const modelName of state.models) grants.delete(grantKey(modelName, name));
        setState({ ...state, keys: state.keys.filter((k) => k.name !== name), grants });
        // 删掉的正好是正在编辑的那一行时退出编辑态, 否则保留原编辑目标。
        setEditing((current) => (current === name ? null : current));
    };

    const grantCount = (keyName: string) =>
        state.models.filter((m) => (state.grants.get(grantKey(m, keyName)) ?? 0) !== 0).length;

    const add = () => {
        // 名称在渠道内唯一, 递增取一个未占用的默认名。
        let n = state.keys.length + 1;
        while (state.keys.some((k) => k.name === `key-${n}`)) n += 1;
        setState({ ...state, keys: [...state.keys, { name: `key-${n}`, key: '', enabled: true }] });
        setDraft({ name: `key-${n}`, key: '' });
        setEditing(`key-${n}`);
    };

    return (
        // 撑满步骤区高度, 凭据列表内部滚动。
        <div className="flex flex-col gap-3 h-full min-h-0">
            <div className="flex items-center justify-between shrink-0">
                <span className="text-sm font-medium">{t('keys')} ({state.keys.length})</span>
                <IconButton onClick={add} className="size-9" tip={t('keyAdd')}>
                    <Plus className="size-4" />
                </IconButton>
            </div>

            <div className="flex-1 min-h-0 overflow-y-auto overscroll-contain space-y-2">
                {state.keys.map((channelKey) => editing === channelKey.name ? (
                    <div key={channelKey.name} className="flex items-center gap-2 rounded-xl border border-border p-2">
                        <Input
                            value={draft.name}
                            onChange={(e) => setDraft({ ...draft, name: e.target.value })}
                            placeholder={t('keyName')}
                            className="rounded-lg h-9 w-32"
                        />
                        <Input
                            value={draft.key}
                            onChange={(e) => setDraft({ ...draft, key: e.target.value })}
                            placeholder={t('apiKey')}
                            className="rounded-lg h-9 flex-1"
                        />
                        <IconButton
                            onClick={() => commit(channelKey.name)}
                            disabled={!draft.name.trim()}
                            className="size-9"
                            tip={t('save')}
                        >
                            <Check className="size-4" />
                        </IconButton>
                    </div>
                ) : (
                    <div key={channelKey.name} className="flex items-center gap-3 rounded-xl border border-border px-3 py-2">
                        <span className="text-sm font-medium truncate w-32">{channelKey.name}</span>
                        <span className="text-xs font-mono text-muted-foreground flex-1 truncate">
                            {channelKey.key ? maskKey(channelKey.key) : t('keyEmpty')}
                        </span>
                        {/* 只显示该凭据自己的授权数, 模型集合是渠道级共享的, 总数对每条凭据都一样, 摆出来会被误读成它已获取的模型数。 */}
                        <span className="text-xs text-muted-foreground tabular-nums">
                            {grantCount(channelKey.name)}
                        </span>
                        <Switch
                            checked={channelKey.enabled}
                            onCheckedChange={(checked) => setState({
                                ...state,
                                keys: state.keys.map((k) => (k.name === channelKey.name ? { ...k, enabled: checked } : k)),
                            })}
                        />
                        {/* 逐条凭据探测: 各凭据在上游被授权的模型不同, 需按凭据分别取回。 */}
                        <IconButton
                            onClick={() => probe(state, setState, channelKey.name)}
                            disabled={pendingKey !== null || !channelKey.key.trim() || !state.base_url.trim()}
                            className="size-8"
                            tip={t('modelRefresh')}
                        >
                            <RefreshCw className={`size-4 ${pendingKey === channelKey.name ? 'animate-spin' : ''}`} />
                        </IconButton>
                        <IconButton
                            onClick={() => { setDraft({ name: channelKey.name, key: channelKey.key }); setEditing(channelKey.name); }}
                            className="size-8"
                            tip={t('edit')}
                        >
                            <Pencil className="size-4" />
                        </IconButton>
                        <IconButton
                            onClick={() => remove(channelKey.name)}
                            className="size-8 hover:text-destructive"
                            tip={t('delete')}
                        >
                            <Trash2 className="size-4" />
                        </IconButton>
                    </div>
                ))}
            </div>
        </div>
    );
}
