import { create } from 'zustand';
import type { NavItem } from '@/components/modules/navbar';

interface SearchState {
    searchTerms: Partial<Record<NavItem, string>>;
    getSearchTerm: (page: NavItem) => string;
    setSearchTerm: (page: NavItem, term: string) => void;
}

export const useSearchStore = create<SearchState>((set, get) => ({
    searchTerms: {},
    getSearchTerm: (page) => get().searchTerms[page] || '',
    setSearchTerm: (page, term) => set((state) => ({
        searchTerms: { ...state.searchTerms, [page]: term }
    })),
}));
