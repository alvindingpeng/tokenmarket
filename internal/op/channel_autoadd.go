package op

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

// autoAddModelLimit 单次自动添加最多并入的新模型数, 防上游返回超长列表拖慢保存并撑爆渠道模型表。
const autoAddModelLimit = 200

// AutoAddChannelModels 渠道保存后按配置自动探测并并入上游模型(第一版规则)。
// 触发: 保存成功后由 handler 调用, 开关取自渠道配置的 model_auto_add; 探测为 best-effort,
// 失败只记日志并继续其余凭据, 不影响已经完成的渠道保存。
// 并入规则:
//   - 名称 trim 后去重(探测结果本身已按名称去重并合并协议位);
//   - 只增不删: 上游未返回的本地模型原样保留, 自动添加永不删除模型;
//   - 授权只并入不覆盖: 探测协议位按位 OR 进该凭据的授权, 管理员手设的协议位不被清掉;
//   - 单次新增上限 autoAddModelLimit, 超出部分跳过并截断;
//   - 授权定稿后重算模型媒体形态(生图位 → kind=image), 并刷新渠道子表与分组缓存。
// 审计: 新增模型以 channel.model-added 落一条记录, 含数量与名称明细(截断时注明)。
func AutoAddChannelModels(ctx context.Context, channelID int, config model.ChannelConfig, client *http.Client, actorID uint) error {
	// 只处理启用且已填 Key 的凭据: 空 Key 探测必失败, 停用的凭据不该被自动启用。
	var keys []model.ChannelKey
	if err := db.GetDB().WithContext(ctx).Where("channel_id = ?", channelID).Find(&keys).Error; err != nil {
		return fmt.Errorf("failed to load channel keys: %w", err)
	}
	keyIDByName := make(map[string]int, len(keys))
	type probeResult struct {
		keyName string
		fetched []model.ChannelFetchModel
	}
	var results []probeResult
	for _, channelKey := range keys {
		if !channelKey.Enabled || strings.TrimSpace(channelKey.Key) == "" {
			continue
		}
		keyIDByName[channelKey.Name] = channelKey.ID
		fetched, err := ProbeChannelModels(client, config, channelKey.Key, ctx)
		if err != nil {
			log.Warnf("auto-add: probe key %q of channel %d failed: %v", channelKey.Name, channelID, err)
			continue
		}
		if len(fetched) == 0 {
			continue
		}
		results = append(results, probeResult{keyName: channelKey.Name, fetched: fetched})
	}
	if len(results) == 0 {
		return nil
	}

	var added []string
	truncated := false
	if err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		newCount := 0
		for _, result := range results {
			keyID, ok := keyIDByName[result.keyName]
			if !ok {
				continue
			}
			keyAdded, keyTruncated, err := mergeFetchedForKey(tx, channelID, keyID, result.fetched, autoAddModelLimit, &newCount)
			if err != nil {
				return err
			}
			added = append(added, keyAdded...)
			truncated = truncated || keyTruncated
		}
		// 授权定稿后媒体形态可能变化(新并入生图位), 与手工保存同一套重算。
		return syncChannelModelKinds(tx, channelID)
	}); err != nil {
		return err
	}

	// 子表缓存与分组路由都要跟上新并入的模型与授权。
	if err := reloadChannelChildren(ctx, channelID); err != nil {
		return err
	}
	if err := groupRefreshCache(ctx); err != nil {
		return err
	}

	if len(added) > 0 || truncated {
		detail := fmt.Sprintf("added=%d: %s", len(added), strings.Join(added, ", "))
		if truncated {
			detail += fmt.Sprintf(" (capped at %d new models)", autoAddModelLimit)
		}
		if err := AuditLogAdd(ctx, actorID, "channel.model-added", fmt.Sprintf("channel#%d", channelID), detail); err != nil {
			log.Warnf("auto-add: audit channel %d failed: %v", channelID, err)
		}
	}
	return nil
}

// mergeFetchedForKey 把单个凭据的探测结果并入渠道: 缺失模型建行, 该凭据上的授权按位 OR。
// newCount 跨凭据累计新增模型数, 达到 limit 即截断并停止处理剩余条目; 已存在模型的授权并入不计入上限。
// 只增不删: 不在此删除任何模型或授权, 上游未返回的本地内容原样保留。
func mergeFetchedForKey(tx *gorm.DB, channelID, keyID int, fetched []model.ChannelFetchModel, limit int, newCount *int) (added []string, truncated bool, err error) {
	var models []model.ChannelModel
	if err := tx.Where("channel_id = ?", channelID).Find(&models).Error; err != nil {
		return nil, false, fmt.Errorf("failed to load channel models: %w", err)
	}
	modelIDByName := make(map[string]int, len(models))
	for _, channelModel := range models {
		modelIDByName[channelModel.Name] = channelModel.ID
	}
	var grants []model.ChannelGrant
	if err := tx.Where("channel_key_id = ?", keyID).Find(&grants).Error; err != nil {
		return nil, false, fmt.Errorf("failed to load channel grants: %w", err)
	}
	protocolByModel := make(map[int]model.Protocol, len(grants))
	for _, grant := range grants {
		protocolByModel[grant.ChannelModelID] = grant.Protocols
	}

	for _, fetchedModel := range fetched {
		name := strings.TrimSpace(fetchedModel.Name)
		if name == "" {
			continue
		}
		protocols := fetchedModel.Protocols & definedProtocols
		modelID, ok := modelIDByName[name]
		if !ok {
			if *newCount >= limit {
				truncated = true
				break
			}
			channelModel := model.ChannelModel{ChannelID: channelID, Name: name}
			if err := tx.Create(&channelModel).Error; err != nil {
				return added, truncated, fmt.Errorf("failed to create channel model %q: %w", name, err)
			}
			modelID = channelModel.ID
			modelIDByName[name] = modelID
			*newCount++
			added = append(added, name)
		}
		if protocols == 0 {
			continue
		}
		current := protocolByModel[modelID]
		merged := current | protocols
		if merged == current {
			continue
		}
		if current == 0 {
			grant := model.ChannelGrant{ChannelModelID: modelID, ChannelKeyID: keyID, Protocols: merged}
			if err := tx.Create(&grant).Error; err != nil {
				return added, truncated, fmt.Errorf("failed to create channel grant: %w", err)
			}
		} else if err := tx.Model(&model.ChannelGrant{}).
			Where("channel_model_id = ? AND channel_key_id = ?", modelID, keyID).
			Update("protocols", merged).Error; err != nil {
			return added, truncated, fmt.Errorf("failed to update channel grant: %w", err)
		}
		protocolByModel[modelID] = merged
	}
	return added, truncated, nil
}

