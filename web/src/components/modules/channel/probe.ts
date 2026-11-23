import { useState } from 'react';
import { toast } from 'sonner';
import { useTranslations } from 'use-intl';
import { useFetchModel } from '@/api/channel';
import { grantKey, toChannelConfig, type ChannelFormState } from './state';

export function modelProbeRequest(state: ChannelFormState, keyName: string) {
    const credential = state.keys.find((key) => key.name === keyName);
    if (!credential) return;

    const key = credential.key.trim();
    const apiSecret = credential.api_secret.trim();
    if (key.includes('••••') || state.dialect === 'subscription') {
        if (!state.channel_id || !credential.savedName) return;
        return { channel_id: state.channel_id, key_name: credential.savedName };
    }
    if (!key || !state.base_url.trim()) return;

    return {
        channel: toChannelConfig(state), key,
        app_id: state.dialect === 'xunfei' ? credential.app_id.trim() : undefined,
        api_secret: state.dialect === 'xunfei' && !apiSecret.includes('••••') ? apiSecret : undefined,
        domain: state.dialect === 'xunfei' ? credential.domain.trim() : undefined,
    };
}

// useModelProbe 按凭据探测上游模型列表, 供模型页与凭据页共用。
// 两侧并发探测与协议位判定都在后端完成, 此处只负责转圈状态, 结果并入表单和结果提示。
export function useModelProbe() {
    const t = useTranslations('channel.form');
    const fetchModel = useFetchModel();
    const [pendingKey, setPendingKey] = useState<string | null>(null); // 正在探测的凭据名称, 只转动该行的图标。

    // probe 探测指定凭据可用的模型, 结果并入模型集合与授权表。
    // 上游未返回但本地已有的模型保留: 静默删除会打断正在使用该模型的路由。
    const probe = async (
        state: ChannelFormState,
        setState: (next: ChannelFormState) => void,
        keyName: string,
    ) => {
        const request = modelProbeRequest(state, keyName);
        if (!request) return;

        setPendingKey(keyName);
        try {
            const fetched = await fetchModel.mutateAsync(request);
            if (fetched.length === 0) {
                toast.warning(t('modelRefreshEmpty'));
                return;
            }
            const models = [...state.models];
            const grants = new Map(state.grants);
            for (const { name, protocols } of fetched) {
                if (!models.includes(name)) models.push(name);
                const mapKey = grantKey(name, keyName);
                grants.set(mapKey, (grants.get(mapKey) ?? 0) | protocols);
            }
            setState({ ...state, models, grants });
            toast.success(t('modelRefreshSuccess', { count: fetched.length }));
        } catch (error) {
            toast.error(t('modelRefreshFailed'), { description: String(error) });
        } finally {
            setPendingKey(null);
        }
    };

    return { probe, pendingKey };
}
