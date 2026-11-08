import { formatPlainMoney } from '@/lib/money';
import type { MarketChannelListing } from '@/api/market';

// 非组件的纯函数放在独立文件里: 与组件同文件导出会破坏 Fast Refresh
// (react-refresh/only-export-components), 组件一改整个页面重挂, 展开状态跟着丢。

/** 徽章键与后端 model.MarketStatus* 常量一一对应。 */
export type MarketStatusKey = '' | 'fx_missing' | 'disabled' | 'key_disabled' | 'breaker' | 'unpriced';

/** marketStatusOf 把一条挂牌归到徽章键: 先看 status_reason, 未定价是"可用但无报价"。 */
export function marketStatusOf(listing: MarketChannelListing): MarketStatusKey {
    if (listing.status_reason) return listing.status_reason;
    if (listing.unpriced) return 'unpriced';
    return '';
}

/** MarketTranslate 是此处需要的最小翻译函数形状; 只依赖 get/has 两点, 便于在组件外调用。 */
export type MarketTranslate = ((key: string, values?: Record<string, string | number>) => string) & {
    has: (key: string) => boolean;
};

/** brandLabel 把品牌键本地化; 后端新增品牌而文案未补齐时回落到原键, 不让整页因缺键抛错。 */
export function brandLabel(t: MarketTranslate, brand: string): string {
    if (brand === 'other') return t('brand.other');
    return t.has('brand.' + brand) ? t('brand.' + brand) : brand;
}

/**
 * priceText 把一条挂牌的用户价渲染成可读文本: 文本模型给"入/出"两档, 媒体模型给每张/每秒。
 *
 * 只显示代表单价会让用户以为"只要 $x", 而四类单价分别计费, 出入价差往往一档以上。
 */
export function priceText(listing: MarketChannelListing, t: MarketTranslate): string {
    if (listing.kind === 'image' && listing.user_media?.per_image) {
        return '$' + formatPlainMoney(listing.user_media.per_image) + ' ' + t('per.image');
    }
    if (listing.kind === 'video' && listing.user_media?.per_second) {
        return '$' + formatPlainMoney(listing.user_media.per_second) + ' ' + t('per.second');
    }
    return '$' + formatPlainMoney(listing.user_price.input) + ' ' + t('per.input') +
        ' · $' + formatPlainMoney(listing.user_price.output) + ' ' + t('per.output');
}

/** metricsLive 判断一条成绩是否可信: 只有 live 口径且有样本才给数字。 */
export function metricsLive(samples: number, source?: string): boolean {
    return samples > 0 && source === 'live';
}
