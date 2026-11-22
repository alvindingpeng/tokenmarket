import { create } from 'zustand';

// TOTPResultKind 决定那张"一次性凭据"面板该说什么话。
//
// 为什么必须有它: 同一块面板此前只服务"启用/解绑"两种结局, 文案里写着
// "所有旧会话已失效, 请重新登录"。恢复码**重新生成**既不吊销会话也不要求重新登录 ——
// 沿用旧文案就是对着用户说假话(他会以为被踢下线了, 而实际上正好好地待在设置页里)。
export type TOTPResultKind = 'enabled' | 'disabled' | 'regenerated';

// Intentionally NOT persisted. Mounted above the authenticated app so expired
// sessions cannot unmount the only copy of freshly generated recovery codes.
export const useTOTPResult = create<{
    result: { recoveryCodes: string[]; kind: TOTPResultKind } | null;
    show: (recoveryCodes: string[], kind?: TOTPResultKind) => void;
    clear: () => void;
}>((set) => ({
    result: null,
    // kind 默认 'enabled': 老调用点(启用流程)不必改动, 行为与修前一致。
    show: (recoveryCodes, kind = 'enabled') => set({ result: { recoveryCodes, kind } }),
    clear: () => set({ result: null }),
}));
