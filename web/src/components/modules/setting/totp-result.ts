import { create } from 'zustand';

// Intentionally NOT persisted. Mounted above the authenticated app so expired
// sessions cannot unmount the only copy of freshly generated recovery codes.
export const useTOTPResult = create<{
    result: { recoveryCodes: string[] } | null;
    show: (recoveryCodes: string[]) => void;
    clear: () => void;
}>((set) => ({
    result: null,
    show: (recoveryCodes) => set({ result: { recoveryCodes } }),
    clear: () => set({ result: null }),
}));
