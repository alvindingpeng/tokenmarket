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

/**
 * metricsNote 生成"这行数字从哪来"的口径说明。
 *
 * 抽成纯函数而不是写在 JSX 里: 这段文案是界面对用户的承诺(实时 / 近一周 / 没数据),
 * 四档各说对一句话比渲染一个 DOM 节点更重要 —— 而它只在展开后可见, SSR 断言不到。
 * 尤其 daily 与 live 必须分开措辞: 把近 7 天统计说成"实时", 比显示 — 更容易误导。
 */
export function metricsNote(
    t: MarketTranslate,
    source: string,
    samples: number
): string {
    if (source === 'live') return t('note.liveMetrics', { count: samples });
    if (source === 'daily') return t('note.dailyMetrics', { count: samples });
    if (source === 'insufficient') return t('note.insufficient', { count: samples });
    return t('note.noSamples');
}

/**
 * qualityBandTexts 把质量带还原成待显示的区间文案数组, 顺序为 成功率 / 延迟 / 可用率。
 *
 * 三条各自独立(后端按维度分别判样本门槛), 故可能只出现一两条; 全空时返回空数组,
 * 调用方据此整段不渲染 —— 不给一个空壳框架。
 */
export function qualityBandTexts(band?: { success?: string; latency?: string; uptime?: string }): string[] {
    if (!band) return [];
    return [band.success, band.latency, band.uptime].filter((text): text is string => Boolean(text));
}
