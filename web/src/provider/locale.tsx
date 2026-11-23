import { useEffect, useState, type ReactNode } from 'react';
import { IntlProvider, type AbstractIntlMessages } from 'use-intl';
import { useSettingStore, type Locale } from '@/stores/setting';
import simplifiedChineseURL from '@/locales/zh_hans.json?url';
import traditionalChineseURL from '@/locales/zh_hant.json?url';
import englishURL from '@/locales/en.json?url';

const locales = {
    zh_hans: { tag: 'zh-Hans', url: simplifiedChineseURL, unavailable: '语言加载失败。', reload: '重新加载' },
    zh_hant: { tag: 'zh-Hant', url: traditionalChineseURL, unavailable: '語言載入失敗。', reload: '重新載入' },
    en: { tag: 'en', url: englishURL, unavailable: 'Could not load the language.', reload: 'Reload' },
} satisfies Record<Locale, { tag: string; url: string; unavailable: string; reload: string }>;

const catalogs = new Map<Locale, Promise<AbstractIntlMessages>>();

function loadMessages(locale: Locale) {
    let request = catalogs.get(locale);
    if (!request) {
        request = fetch(locales[locale].url).then(async (response) => {
            if (!response.ok) throw new Error(`Locale request failed: ${response.status}`);
            return await response.json() as AbstractIntlMessages;
        }).catch((error: unknown) => {
            catalogs.delete(locale);
            throw error;
        });
        catalogs.set(locale, request);
    }
    return request;
}

export function LocaleProvider({ children }: { children: ReactNode }) {
    const preference = useSettingStore((state) => state.locale);
    const locale = Object.hasOwn(locales, preference) ? preference : 'zh_hans';
    const [active, setActive] = useState<{ locale: Locale; messages: AbstractIntlMessages }>();
    const [failed, setFailed] = useState<Locale>();

    useEffect(() => {
        let current = true;
        void loadMessages(locale).then(
            (messages) => {
                if (!current) return;
                setActive({ locale, messages });
                setFailed(undefined);
            },
            () => {
                if (current) setFailed(locale);
            },
        );
        return () => { current = false; };
    }, [locale]);

    const displayed = locales[active?.locale ?? locale];
    const showError = failed === locale;

    useEffect(() => {
        document.documentElement.lang = displayed.tag;
        if (showError) document.getElementById('initial-loader')?.remove();
    }, [displayed.tag, showError]);

    return (
        <>
            {active && (
                <IntlProvider locale={displayed.tag} messages={active.messages} timeZone="Asia/Shanghai">
                    {children}
                </IntlProvider>
            )}
            {showError && (
                <div role="alert" className="fixed inset-x-4 bottom-4 z-50 mx-auto flex w-fit items-center gap-3 rounded-md border bg-background p-3 text-sm shadow">
                    <span>{displayed.unavailable}</span>
                    <button type="button" className="underline" onClick={() => window.location.reload()}>
                        {displayed.reload}
                    </button>
                </div>
            )}
        </>
    );
}
