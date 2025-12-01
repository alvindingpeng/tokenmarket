import { ChannelType } from '@/api/endpoints/channel';

export function getTypeLabel(type: ChannelType) {
    switch (type) {
        case ChannelType.OpenAIChat:
            return 'OpenAI Chat';
        case ChannelType.OpenAIResponse:
            return 'OpenAI Response';
        case ChannelType.Anthropic:
            return 'Anthropic';
        case ChannelType.OneAPI:
            return 'OneAPI';
        default:
            return 'Unknown';
    }
}
