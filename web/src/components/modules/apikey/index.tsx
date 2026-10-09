import { SettingAPIKey } from '@/components/modules/setting/APIKey';

// ApikeyPage 独立的 API 密钥页面: 从设置页拆出到导航, 所有角色可用;
// 面板本体复用设置页的密钥卡片, 增删改与统计行为保持一致。
export default function ApikeyPage() {
    return (
        <div className="h-full min-h-0 overflow-y-auto overscroll-contain rounded-t-3xl pb-24 md:pb-4">
            <div className="columns-1 gap-4 md:columns-2 *:mb-4 *:break-inside-avoid">
                <SettingAPIKey />
            </div>
        </div>
    );
}
