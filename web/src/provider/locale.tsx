'use client';

import { useEffect, useState, type ReactNode } from 'react';
import { NextIntlClientProvider } from 'next-intl';
import { useSettingStore, type Locale } from '@/stores/setting';

import zhMessages from '../../public/locale/zh.json';
import enMessages from '../../public/locale/en.json';

const messages: Record<Locale, typeof zhMessages> = {
    zh: zhMessages,
    en: enMessages,
};

export function LocaleProvider({ children }: { children: ReactNode }) {
    const { locale } = useSettingStore();
    const [currentLocale, setCurrentLocale] = useState<Locale>('zh');

    useEffect(() => {
        setCurrentLocale(locale);
    }, [locale]);

    return (
        <NextIntlClientProvider
            locale={currentLocale}
            messages={messages[currentLocale]}
            timeZone="Asia/Shanghai"
        >
            {children}
        </NextIntlClientProvider>
    );
}

