import { describe, expect, it } from 'vitest';
import { modelProbeRequest } from './probe';
import { emptyFormState, fromChannel, toChannelDetail } from './state';

const saved = fromChannel({
    ...toChannelDetail(emptyFormState, 17),
    base_url: 'https://upstream.example',
    keys: [{ name: 'primary', key: '••••••••', enabled: true }],
});

describe('model probe credentials', () => {
    it('references the saved credential from either refresh button', () => {
        const request = modelProbeRequest(saved, 'primary');
        expect(request).toEqual({ channel_id: 17, key_name: 'primary' });
    });

    it('uses the saved name until a draft rename is persisted', () => {
        const state = { ...saved, keys: [{ ...saved.keys[0], name: 'renamed' }] };
        expect(modelProbeRequest(state, 'renamed')).toMatchObject({ key_name: 'primary' });
    });

    it('probes a newly entered key and uses the draft channel settings', () => {
        const state = { ...saved, base_url: 'https://draft.example', keys: [{ ...saved.keys[0], key: ' test-new-key ' }] };
        const request = modelProbeRequest(state, 'primary');
        expect(request).toMatchObject({ channel: { base_url: 'https://draft.example' }, key: 'test-new-key' });
        expect(request).not.toHaveProperty('key_name');
    });

    it('allows a saved subscription account without a draft URL or key value', () => {
        const state = { ...saved, dialect: 'subscription' as const, base_url: '', keys: [{ ...saved.keys[0], key: '' }] };
        expect(modelProbeRequest(state, 'primary')).toMatchObject({ channel_id: 17, key_name: 'primary' });
    });
});
