import { useState } from 'react';
import { toast } from 'sonner';
import { useTranslations } from 'use-intl';
import { useFetchModel } from '@/api/channel';
import { grantKey, toChannelConfig, type ChannelFormState } from './state';

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
        // 模型页的刷新按钮不按凭据禁用, 选中的凭据可能还没填 Key, 在此挡掉空请求。
        const isSubscription = state.dialect === 'subscription';
        // 订阅渠道的凭据是登录后导入的账号, 表单里没有 key 可填 —— 凭据由后端按 channel_id 取。
        // 这里若沿用"key 为空就 return", 订阅渠道的拉取模型按钮会静默失效(点了没反应)。
        const channelKey = state.keys.find((k) => k.name === keyName);
        if (!isSubscription && (!channelKey || channelKey.key.trim() === '')) return;

        setPendingKey(keyName);
        try {
            // 探测用的地址, 路径, 代理和过滤表达式必须和保存后生效的完全一致, 否则这里探到的模型
            // 与实际转发时能用的模型会不一样; 故与提交共用同一份配置, 探测用不上的字段由后端忽略。
            const fetched = await fetchModel.mutateAsync({
                channel: toChannelConfig(state),
                // 订阅渠道不传 key(也没得传), 改传 channel_id 让后端取库里的账号凭据。
                key: isSubscription ? undefined : (channelKey?.key ?? '').trim(),
                channel_id: isSubscription ? state.channel_id : undefined,
                app_id: state.dialect === 'xunfei' ? (channelKey?.app_id ?? '').trim() : undefined,
                api_secret: state.dialect === 'xunfei' ? (channelKey?.api_secret ?? '').trim() : undefined,
                domain: state.dialect === 'xunfei' ? (channelKey?.domain ?? '').trim() : undefined,
            });
            if (fetched.length === 0) {
                toast.warning(t('modelRefreshEmpty'));
                return;
            }
            const models = [...state.models];
            const grants = new Map(state.grants);
            for (const { name, protocols } of fetched) {
                if (!models.includes(name)) models.push(name);
                const mapKey = grantKey(name, channelKey?.name ?? keyName);
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
