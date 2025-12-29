export const SW_MESSAGE_TYPE = {
    SKIP_WAITING: 'SKIP_WAITING',
    CLEAR_CACHE: 'CLEAR_CACHE',
    CACHE_CLEARED: 'CACHE_CLEARED',
} as const;

export type SwMessageType = (typeof SW_MESSAGE_TYPE)[keyof typeof SW_MESSAGE_TYPE];

// Keep in sync with `web/public/sw.js`
export const SWITCHYARD_CACHE_PREFIX = 'switchyard-';
// Font cache is version-independent and should persist across updates
export const SWITCHYARD_FONT_CACHE_NAME = 'switchyard-font';

export function isSwitchyardCacheName(name: string) {
    return name.startsWith(SWITCHYARD_CACHE_PREFIX);
}

export function isFontCacheName(name: string) {
    return name === SWITCHYARD_FONT_CACHE_NAME;
}


