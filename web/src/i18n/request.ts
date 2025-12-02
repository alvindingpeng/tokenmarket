import { getRequestConfig } from 'next-intl/server';

export default getRequestConfig(async () => {
    // Default locale for static export
    const locale = 'zh';

    return {
        locale,
        messages: (await import(`../../public/locale/${locale}.json`)).default,
    };
});

