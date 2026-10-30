import { create } from 'zustand';

// TicketFocusState 通知跳转带来的工单聚焦请求。
// 跳转发生在通知组件, 打开工单详情发生在工单模块内部, 两者互不挂载,
// 用一个小 store 传递「落地后打开哪张工单」, 不用把选中态抬到全局路由层。
interface TicketFocusState {
    focusId: number | null; // 待打开工单号; 工单模块渲染消费, 用户离开详情时清空。
    requestFocus: (id: number) => void;
    clearFocus: () => void;
}

export const useTicketFocusStore = create<TicketFocusState>()((set) => ({
    focusId: null,
    requestFocus: (id) => set({ focusId: id }),
    clearFocus: () => set({ focusId: null }),
}));
