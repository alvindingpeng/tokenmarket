import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';

// ModelAutoAddRule 是「当前登录用户为某个模型单独设的」自动添加条件。
// 所有字段都是字符串: 空串表示未单独设置, 该字段回落到系统设置里的全局默认。
// 后端按 (user_id, name) 存放, 每个用户看到的都是自己设的那一份。
export type ModelAutoAddRule = {
    // on = 该模型强制并入(跳过其余条件); off = 该模型永不并入; 空 = 跟随全局/渠道开关。
    mode: '' | 'on' | 'off';
    price_min: string; // 参考价下限(每百万 token), 空=回落全局。
    price_max: string; // 参考价上限, 空=回落全局。
    markup_min: string; // 倍率下限, 空=回落全局。
    markup_max: string; // 倍率上限, 空=回落全局。
    exclude: string; // 排除正则, 空=回落全局。
    include: string; // 白名单正则, 空=回落全局。
    fill_price: '' | 'on' | 'off'; // 并入时是否按参考价预填供货价, 空=回落全局。
};

// ModelAutoAddRuleEntry 是后端下发的形状: 条件本体加上它归属的模型。
export type ModelAutoAddRuleEntry = ModelAutoAddRule & {
    user_id: number;
    name: string;
};

// 条件全为空等价于「没设」, 后端会直接删行, 界面据此判断要不要显示徽标。
export function ruleIsEmpty(rule: ModelAutoAddRule | undefined): boolean {
    if (!rule) return true;
    return (
        rule.mode === '' &&
        rule.price_min === '' &&
        rule.price_max === '' &&
        rule.markup_min === '' &&
        rule.markup_max === '' &&
        rule.exclude === '' &&
        rule.include === '' &&
        rule.fill_price === ''
    );
}

export const modelAutoAddRuleQueryOptions = {
    queryKey: ['models', 'auto-add-rules'],
    queryFn: () => apiRequest<ModelAutoAddRuleEntry[]>('/api/v1/model/auto-add-rule/list'),
};

// useModelAutoAddRules 取当前用户设置过的全部模型级条件, 按模型名索引, 供卡片与编辑器共用一次请求。
export function useModelAutoAddRules() {
    return useQuery({
        ...modelAutoAddRuleQueryOptions,
        // 这份数据只在用户自己编辑时变, 没有后台同步, 落盘后失效即可, 不必轮询。
        staleTime: Infinity,
        select: (entries: ModelAutoAddRuleEntry[]) => {
            const map = new Map<string, ModelAutoAddRule>();
            for (const entry of entries) {
                map.set(entry.name, {
                    mode: entry.mode,
                    price_min: entry.price_min,
                    price_max: entry.price_max,
                    markup_min: entry.markup_min,
                    markup_max: entry.markup_max,
                    exclude: entry.exclude,
                    include: entry.include,
                    fill_price: entry.fill_price,
                });
            }
            return map;
        },
    });
}

// useSaveModelAutoAddRule 保存单个模型的条件; 全空条件由后端归一成删除。
export function useSaveModelAutoAddRule() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (input: { name: string; rule: ModelAutoAddRule }) =>
            apiRequest<ModelAutoAddRuleEntry>('/api/v1/model/auto-add-rule/save', {
                method: 'POST',
                body: { name: input.name, ...input.rule },
            }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: modelAutoAddRuleQueryOptions.queryKey }),
    });
}
