import { createContext, useContext, useEffect, useState, type ReactNode } from 'react';

type Theme = 'light' | 'dark' | 'system' | 'white' | 'compact';
type ResolvedTheme = 'light' | 'dark';

interface ThemeContextValue {
    theme: Theme;
    resolvedTheme: ResolvedTheme;
    setTheme: (theme: string) => void;
}

const ThemeContext = createContext<ThemeContextValue | null>(null);

export function ThemeProvider({ children }: { children: ReactNode }) {
    const [theme, setThemeState] = useState<Theme>(() => {
        const storedTheme = localStorage.getItem('theme');
        return storedTheme === 'light' || storedTheme === 'dark' || storedTheme === 'system' || storedTheme === 'white' || storedTheme === 'compact'
            ? storedTheme
            : 'system';
    });
    const [systemTheme, setSystemTheme] = useState<ResolvedTheme>(() =>
        window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
    );
    const resolvedTheme = theme === 'system' ? systemTheme : theme === 'dark' ? 'dark' : 'light';

    useEffect(() => {
        const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)');

        const handleChange = (event: MediaQueryListEvent) => {
            setSystemTheme(event.matches ? 'dark' : 'light');
        };

        mediaQuery.addEventListener('change', handleChange);
        return () => mediaQuery.removeEventListener('change', handleChange);
    }, []);

    useEffect(() => {
        const root = document.documentElement;
        root.classList.toggle('dark', resolvedTheme === 'dark');
        root.classList.toggle('theme-white', theme === 'white' || theme === 'compact');
        root.classList.toggle('theme-compact', theme === 'compact');
        root.style.colorScheme = resolvedTheme;
        document.querySelector('meta[name="theme-color"]')?.setAttribute(
            'content',
            theme === 'white' || theme === 'compact' ? '#f7f8fa' : resolvedTheme === 'dark' ? '#413a2c' : '#eae9e3'
        );
        localStorage.setItem('theme', theme);
    }, [resolvedTheme, theme]);

    const setTheme = (value: string) => {
        if (value === 'light' || value === 'dark' || value === 'system' || value === 'white' || value === 'compact') {
            setThemeState(value);
        }
    };

    return (
        <ThemeContext value={{ theme, resolvedTheme, setTheme }}>
            {children}
        </ThemeContext>
    );
}

export function useTheme() {
    const context = useContext(ThemeContext);
    if (!context) {
        throw new Error('useTheme must be used within ThemeProvider');
    }
    return context;
}
