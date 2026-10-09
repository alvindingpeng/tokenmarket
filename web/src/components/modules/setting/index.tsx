import { useAuthStore } from '@/api/user';
import { SettingAppearance } from './Appearance';
import { SettingSystem } from './System';
import { SettingSystemInfo } from './SystemInfo';
import { SettingPlatform } from './Platform';
import { SettingLLMPrice } from './LLMPrice';
import { SettingAccount } from './Account';
import { SettingInfo } from './Info';
import { SettingLog } from './Log';
import { SettingOps } from './Ops';
import { SettingBackup } from './Backup';

// Setting 渲染设置页面正文; 系统设置、全局价表、备份与更新属管理后台, 仅管理员可见。
export function Setting() {
    const role = useAuthStore((state) => state.role);
    const isAdmin = role === 'admin';

    return (
        <div className="h-full min-h-0 overflow-y-auto overscroll-contain rounded-t-3xl pb-24 md:pb-4">
            <div className="columns-1 gap-4 md:columns-2 *:mb-4 *:break-inside-avoid">
                {isAdmin && <SettingInfo />}
                <SettingAppearance />
                <SettingAccount />
                {isAdmin && <SettingSystemInfo />}
                {isAdmin && <SettingSystem />}
                {isAdmin && <SettingPlatform />}
                {isAdmin && <SettingLog />}
                {isAdmin && <SettingOps />}
                {isAdmin && <SettingLLMPrice />}
                {isAdmin && <SettingBackup />}
            </div>
        </div>
    );
}
