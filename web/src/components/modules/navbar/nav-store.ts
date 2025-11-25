import { create } from 'zustand'
import { persist } from 'zustand/middleware'

export type NavItem = 'home' | 'channel' | 'model' | 'group' | 'key' | 'setting'

interface NavState {
    activeItem: NavItem
    setActiveItem: (item: NavItem) => void
}

export const useNavStore = create<NavState>()(
    persist(
        (set) => ({
            activeItem: 'home',
            setActiveItem: (item) => set({ activeItem: item }),
        }),
        {
            name: 'nav-storage',
        }
    )
)
