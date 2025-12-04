'use client';

import { PageWrapper } from '@/components/common/PageWrapper';
import { SettingAppearance } from './Appearance';
import { SettingSystem } from './System';
import { SettingAPIKey } from './APIKey';
import { SettingLLMPrice } from './LLMPrice';

export function Setting() {
    return (
        <PageWrapper className="columns-1 md:columns-2 gap-4 [&>div]:mb-4 [&>div]:break-inside-avoid">
            <div>
                <SettingAppearance key="setting-appearance" />
            </div>
            <div>
                <SettingSystem key="setting-system" />
            </div>
            <div>
                <SettingAPIKey key="setting-apikey" />
            </div>
            <div>
                <SettingLLMPrice key="setting-llmprice" />
            </div>
        </PageWrapper>
    );
}
