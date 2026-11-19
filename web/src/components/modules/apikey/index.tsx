import { SettingAPIKey } from '@/components/modules/setting/APIKey';

// ApikeyPage 独立的 API 密钥页面: 从设置页拆出到导航, 所有角色可用;
// 面板本体复用设置页的密钥卡片, 增删改与统计行为保持一致。
export default function ApikeyPage() {
    return (
        <div className="h-full min-h-0 overflow-y-auto overscroll-contain rounded-t-3xl px-1 md:px-0 pb-24 md:pb-4">
            {/* 不用 columns 多列瀑布流: 本页只有一张密钥面板, columns 布局会让 motion layout 子项
                测量出的列宽塌陷成内容宽(桌面/iPad 上缩成小方块)。改用普通块级容器 + w-full,
                面板始终撑满可用宽度; 面板内部列表自身滚动, 高度由内容决定。 */}
            <div className="mx-auto w-full max-w-6xl space-y-4">
                <SettingAPIKey />
            </div>
        </div>
    );
}
