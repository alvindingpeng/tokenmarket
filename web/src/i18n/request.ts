import { getRequestConfig } from 'next-intl/server';

export default getRequestConfig(async () => {
    // Static for now, we'll change this later
    const locale = 'zh';

    return {
        locale,
        messages: (await import(`../../public/locale/${locale}.json`)).default
    };
});