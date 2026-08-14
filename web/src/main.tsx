import { createRoot } from 'react-dom/client';
import { AppContainer } from '@/components/app';
import { TooltipProvider } from '@/components/animate-ui/components/animate/tooltip';
import { Toaster } from '@/components/ui/sonner';
import { LocaleProvider } from '@/provider/locale';
import QueryProvider from '@/provider/query';
import { ThemeProvider } from '@/provider/theme';
import './globals.css';

createRoot(document.getElementById('root')!).render(
  <>
    <ThemeProvider>
      <QueryProvider>
        <LocaleProvider>
          <TooltipProvider>
            <AppContainer />
            <Toaster />
          </TooltipProvider>
        </LocaleProvider>
      </QueryProvider>
    </ThemeProvider>
  </>,
);

// 生产环境在页面加载完成后注册应用根作用域的 Service Worker。
if (import.meta.env.PROD && 'serviceWorker' in navigator) {
  window.addEventListener('load', () => {
    void navigator.serviceWorker.register('/sw.js', { scope: '/' }).catch((error: unknown) => {
      console.error('Service Worker registration failed', error);
    });
  });
}
