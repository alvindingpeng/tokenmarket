package op

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/utils/cache"
	"gorm.io/gorm"
)

var (
	groupCache     = cache.New[int, model.Group](16) // 按主键保存完整分组配置。
	groupNameIndex = cache.New[string, int](16)      // 归属+客户端模型名对应的分组主键; 分组名按归属唯一, 跨用户可重名。
)

// groupIndexKey 分组名索引键: 分组名在归属内唯一, 解析必须带归属。
func groupIndexKey(userID uint, name string) string {
	return fmt.Sprintf("%d:%s", userID, name)
}

// GroupList 返回缓存中的全部分组, 成员已补齐界面展示所需的名称与可用性, 按名称定序。
// 不含实时路由状态: 路由状态由 Relay 持有, 而 Relay 依赖本包, 故由处理器在返回前补齐。
// 定序是为 API Key 面板的模型选择器: 那里没有排序开关, 而缓存遍历顺序随机;
// 分组页自带升降序开关, 会按开关重排, 不依赖此顺序。
func GroupList(scope model.Scope) []model.Group {
	groups := make([]model.Group, 0, groupCache.Len())
	for _, group := range groupCache.GetAll() {
		// 归属过滤: 严格按用户隔离, 管理员也只看自有分组(分组不跨用户共享)。
		if group.UserID != scope.ID {
			continue
		}
		groups = append(groups, groupSnapshot(group, scope))
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Name != groups[j].Name {
			return groups[i].Name < groups[j].Name
		}
		return groups[i].ID < groups[j].ID
	})
	return groups
}

// GroupGet 返回指定分组的读取副本, 成员已补齐界面展示所需的名称与可用性。
// 不含实时路由状态: 与 GroupList 同理, 由处理器在返回前补齐。
func GroupGet(id int, scope model.Scope) (model.Group, error) {
	group, ok := groupCache.Get(id)
	if !ok || group.UserID != scope.ID {
		return model.Group{}, fmt.Errorf("group not found")
	}
	return groupSnapshot(group, scope), nil
}

// GroupOwnerID 返回分组归属用户, 供事件流按访问者过滤; 分组不存在时 exists 为假。
func GroupOwnerID(id int) (uint, bool) {
	group, ok := groupCache.Get(id)
	if !ok {
		return 0, false
	}
	return group.UserID, true
}

// GroupListModel 返回缓存中的全部分组模型名, 按名称定序。
// 两个消费方都不提供排序开关: /v1/models 由第三方客户端直接展示, API Key 面板按返回顺序列出可用模型,
// 而缓存遍历顺序随机, 故顺序须由此处定稿。
func GroupListModel(scope model.Scope) []string {
	models := make([]string, 0, groupCache.Len())
	for _, group := range groupCache.GetAll() {
		if group.UserID != scope.ID {
			continue
		}
		models = append(models, group.Name)
	}
	sort.Strings(models)
	return models
}

// GroupGetByName 返回客户端模型名称对应的分组配置, 供转发选路使用。
// 渠道或凭据被禁用及授权两侧缺失的成员不参与选路, 重新可用后会在下一轮读取时自动恢复。
func GroupGetByName(name string, ownerID uint) (model.Group, error) {
	groupID, ok := groupNameIndex.Get(groupIndexKey(ownerID, name))
	if !ok {
		return model.Group{}, fmt.Errorf("group not found")
	}
	group, ok := groupCache.Get(groupID)
	if !ok {
		return model.Group{}, fmt.Errorf("group not found")
	}
	// 转发视图: 只要可用成员与归属, 展示字段按归属者口径补齐。
	group = groupSnapshot(group, model.Scope{ID: ownerID, Role: model.RoleUser})
	group.Items = slices.DeleteFunc(group.Items, func(item model.GroupItem) bool { return !item.Available })
	return group, nil
}

// GroupCreate 创建分组及其成员并刷新缓存, 返回创建后的分组。
// 成员的提交顺序即优先级顺序。
func GroupCreate(req *model.GroupCreateRequest, scope model.Scope, ctx context.Context) (*model.Group, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("group name is required")
	}
	group := model.Group{
		UserID:      scope.ID,
		Name:        name,
		Mode:        req.Mode,
		RelayConfig: req.RelayConfig,
		Items:       make([]model.GroupItem, len(req.Items)),
	}
	if group.Mode == "" {
		group.Mode = model.GroupModeManual
	}
	model.NormalizeGroupRelayConfigForMode(&group.RelayConfig, group.Mode)
	// 新建分组的成员主键由数据库分配, 客户端不可能预先知道, 因此创建时不可能指定有效的灰度成员;
	// 灰度只能在分组存在后设置, 这里一律清掉, 免得留下一个永远指不到的悬空 ID。
	group.RelayConfig.CanaryItemID = 0
	for i, item := range req.Items {
		if err := validateGroupItemAccess(item.ChannelGrantID, scope); err != nil {
			return nil, err
		}
		group.Items[i] = model.GroupItem{ChannelGrantID: item.ChannelGrantID, Priority: i + 1}
	}
	if err := db.GetDB().WithContext(ctx).Create(&group).Error; err != nil {
		return nil, err
	}
	// 手动模式总要指向一个当前成员: 未提交选择时默认首个成员, 否则请求会因找不到成员而一直等待。
	if group.Mode == model.GroupModeManual && len(group.Items) > 0 {
		group.ActiveItemID = group.Items[0].ID
		if err := db.GetDB().WithContext(ctx).Model(&model.Group{}).
			Where("id = ?", group.ID).Update("active_item_id", group.ActiveItemID).Error; err != nil {
			return nil, err
		}
	}
	groupCache.Set(group.ID, group)
	groupNameIndex.Set(groupIndexKey(group.UserID, group.Name), group.ID)
	snapshot := groupSnapshot(group, scope)
	return &snapshot, nil
}

// validateGroupItemAccess 校验分组成员准入: 一律只接受「已发布 + 已过审 + 已上架」渠道的授权,
// 对所有角色一视同仁(含渠道归属者与管理员)。
//
// 过审位与对外候选(ChannelGrantCandidates)对齐: 候选只列 approved, 若此处放行 draft/stale,
// 就会出现"广场/分组页看不到这条候选, 但直接提交主键能加进去"的绕过路径 —— 审核闸口形同虚设。
// 与快照的差异是有意的: 快照只查 shared+listed, 复审转 stale 的存量成员仍保留(那是不减少暴露),
// 而新增成员是新增暴露, 必须过审。
func validateGroupItemAccess(grantID int, scope model.Scope) error {
	grant, ok := channelGrantCache.Get(grantID)
	if !ok {
		return fmt.Errorf("channel grant not found")
	}
	parts, ok := grantPartsOf(grant)
	if !ok {
		return fmt.Errorf("channel grant not found")
	}
	if parts.Channel.Shared && parts.Channel.ReviewStatus == model.ChannelReviewApproved && parts.ChannelModel.Listed {
		return nil
	}
	return fmt.Errorf("channel grant not found")
}

// GroupUpdate 更新分组配置, 成员和当前成员，并返回刷新后的分组。
func GroupUpdate(id int, req *model.GroupUpdateRequest, scope model.Scope, ctx context.Context) (*model.Group, error) {
	oldGroup, ok := groupCache.Get(id)
	if !ok || oldGroup.UserID != scope.ID {
		return nil, fmt.Errorf("group not found")
	}
	oldName := oldGroup.Name

	var selectFields []string
	updates := model.Group{ID: id}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, fmt.Errorf("group name is required")
		}
		selectFields = append(selectFields, "name")
		updates.Name = name
	}
	if req.Mode != nil {
		selectFields = append(selectFields, "mode")
		updates.Mode = *req.Mode
	}
	if req.RelayConfig != nil {
		config := *req.RelayConfig
		mode := oldGroup.Mode
		if req.Mode != nil {
			mode = *req.Mode
		}
		model.NormalizeGroupRelayConfigForMode(&config, mode)
		selectFields = append(selectFields, "relay_config")
		updates.RelayConfig = config
	}

	var group model.Group
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(selectFields) > 0 {
			if err := tx.Model(&model.Group{}).Where("id = ?", id).Select(selectFields).Updates(&updates).Error; err != nil {
				return fmt.Errorf("failed to update group: %w", err)
			}
		}
		if req.Items != nil {
			for _, item := range *req.Items {
				if err := validateGroupItemAccess(item.ChannelGrantID, scope); err != nil {
					return err
				}
			}
			if err := syncGroupItems(tx, id, *req.Items); err != nil {
				return err
			}
		}
		if err := tx.Preload("Items").First(&group, id).Error; err != nil {
			return fmt.Errorf("failed to load updated group: %w", err)
		}
		// 灰度成员可能刚被移出分组: 留着悬空的 canary_item_id 会让界面显示"灰度开着"却永远不生效,
		// 也会让人误以为新渠道还在接流量。成员集合定稿后立即对齐, 与分组成员保持同一事务。
		if group.RelayConfig.CanaryItemID != 0 &&
			!slices.ContainsFunc(group.Items, func(item model.GroupItem) bool { return item.ID == group.RelayConfig.CanaryItemID }) {
			group.RelayConfig.CanaryItemID = 0
			cleared := model.Group{ID: id, RelayConfig: group.RelayConfig}
			if err := tx.Model(&model.Group{}).Where("id = ?", id).Select("relay_config").Updates(&cleared).Error; err != nil {
				return fmt.Errorf("failed to clear canary item: %w", err)
			}
		}
		// 当前成员在成员集合定稿后才写入: syncGroupItems 会清空指向已删除成员的当前成员,
		// 先写会被它覆盖; 归属校验同样只对最终集合成立。
		if req.ActiveItemID != nil {
			if *req.ActiveItemID != 0 && !slices.ContainsFunc(group.Items, func(item model.GroupItem) bool { return item.ID == *req.ActiveItemID }) {
				return fmt.Errorf("group item not found")
			}
			if err := tx.Model(&model.Group{}).Where("id = ?", id).Update("active_item_id", *req.ActiveItemID).Error; err != nil {
				return fmt.Errorf("failed to update active item: %w", err)
			}
			group.ActiveItemID = *req.ActiveItemID
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sortGroupItems(group.Items)
	groupCache.Set(group.ID, group)
	groupNameIndex.Set(groupIndexKey(group.UserID, group.Name), group.ID)
	if oldName != group.Name {
		groupNameIndex.Del(groupIndexKey(group.UserID, oldName))
	}
	snapshot := groupSnapshot(group, scope)
	return &snapshot, nil
}

// syncGroupItems 按提交的成员集合新增, 重排与删除分组成员。
// 成员在分组内按渠道授权唯一, 该授权作为匹配依据, 由此已有成员保留其主键:
// 主键被分组的当前成员和 Relay 的路由状态引用, 换主键会让人工选择与冷却记录失效。
// 优先级一律按提交顺序重写, 前端只需提交当前排列, 无需自行算出哪些成员的顺序发生了变化。
func syncGroupItems(tx *gorm.DB, groupID int, requested []model.GroupItemInput) error {
	var existing []model.GroupItem
	if err := tx.Where("group_id = ?", groupID).Find(&existing).Error; err != nil {
		return fmt.Errorf("failed to load group items: %w", err)
	}
	existingByGrant := make(map[int]model.GroupItem, len(existing))
	for _, item := range existing {
		existingByGrant[item.ChannelGrantID] = item
	}

	for priority, requestedItem := range requested {
		current, ok := existingByGrant[requestedItem.ChannelGrantID]
		if !ok {
			newItem := model.GroupItem{GroupID: groupID, ChannelGrantID: requestedItem.ChannelGrantID, Priority: priority + 1}
			if err := tx.Create(&newItem).Error; err != nil {
				return fmt.Errorf("failed to create group item: %w", err)
			}
			continue
		}
		if current.Priority != priority+1 {
			if err := tx.Model(&model.GroupItem{}).Where("id = ?", current.ID).
				Update("priority", priority+1).Error; err != nil {
				return fmt.Errorf("failed to update group item: %w", err)
			}
		}
		delete(existingByGrant, requestedItem.ChannelGrantID)
	}

	deletedIDs := make([]int, 0, len(existingByGrant))
	for _, item := range existingByGrant {
		deletedIDs = append(deletedIDs, item.ID)
	}
	if len(deletedIDs) == 0 {
		return nil
	}
	// 被删掉的成员可能正是当前人工指定的成员, 需一并清空, 否则分组会指向一个已不存在的成员。
	if err := tx.Model(&model.Group{}).
		Where("id = ? AND active_item_id IN ?", groupID, deletedIDs).
		Update("active_item_id", 0).Error; err != nil {
		return fmt.Errorf("failed to clear active item: %w", err)
	}
	if err := tx.Delete(&model.GroupItem{}, deletedIDs).Error; err != nil {
		return fmt.Errorf("failed to delete group items: %w", err)
	}
	return nil
}

// GroupAddItems 把一批渠道授权**追加**到若干分组末尾, 不整体替换成员集合。
//
// 为什么必须单独有这条写路径: 广场"把模型加进配置"由前端用 GroupUpdate 的 items 整体替换来做,
// 而整体替换要求调用方先持有准确的当前集合。广场的卡片数据来自 20 秒轮询的缓存, 用户在弹层里
// 停留期间另一个会话(或另一个标签页)可能刚改过同一个分组 —— 此时前端算出的合并结果会漏掉那次
// 新增, 提交后那条成员凭空消失, 且没有任何报错。改成后端"读出当前集合 → 追加 → 写回"后,
// 并发写不再依赖调用方手里的旧快照, 两个会话各加各的, 结果都保留。
//
// 语义取舍:
//   - 只追加不重排: 既有成员的顺序与优先级一律不动, 新成员按提交顺序接在末尾。
//     重排属于分组页的编辑操作, 不该被"加一个模型"顺手改掉。
//   - 已存在即跳过而非报错: 重复点击、两个标签页各提交一次都是正常操作, 报 409 只会让用户困惑。
//   - 归属校验与准入校验复用同一套: 分组必须属于当前身份(管理员也改不了别人的分组),
//     授权必须「已发布 + 已过审 + 已上架」, 与 GroupUpdate/GroupCreate 同一口径。
//
// 返回每个分组各自的 added/skipped 与追加后的完整分组, 供界面逐条展示结果。
func GroupAddItems(req *model.GroupAddItemsRequest, scope model.Scope, ctx context.Context) ([]model.GroupAddItemsResult, error) {
	// 先在事务外把准入校验做完: 授权不存在或不可售时直接失败, 不留下半写状态。
	// 去重是必要的 —— 请求里重复给出同一个授权时, 追加两次会在事务里撞唯一索引。
	grantIDs := make([]int, 0, len(req.ChannelGrantIDs))
	seenGrant := make(map[int]bool, len(req.ChannelGrantIDs))
	for _, grantID := range req.ChannelGrantIDs {
		if seenGrant[grantID] {
			continue
		}
		seenGrant[grantID] = true
		if err := validateGroupItemAccess(grantID, scope); err != nil {
			return nil, err
		}
		grantIDs = append(grantIDs, grantID)
	}

	// 目标分组同样去重: 重复提交同一分组会在结果里出现两条自相矛盾的行。
	groupIDs := make([]int, 0, len(req.GroupIDs))
	seenGroup := make(map[int]bool, len(req.GroupIDs))
	for _, groupID := range req.GroupIDs {
		if seenGroup[groupID] {
			continue
		}
		seenGroup[groupID] = true
		groupIDs = append(groupIDs, groupID)
	}

	results := make([]model.GroupAddItemsResult, 0, len(groupIDs))
	updated := make([]model.Group, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		oldGroup, ok := groupCache.Get(groupID)
		if !ok || oldGroup.UserID != scope.ID {
			return nil, fmt.Errorf("group not found")
		}

		result := model.GroupAddItemsResult{
			GroupID: groupID,
			Added:   make([]int, 0, len(grantIDs)),
			Skipped: make([]int, 0, len(grantIDs)),
		}
		var group model.Group
		err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// 在事务内重新读当前成员, 而不是用缓存里的 oldGroup.Items:
			// 缓存与本事务之间可能已经插入过一次并发追加, 用缓存算差集会漏成员(即本函数要解决的 bug)。
			var existing []model.GroupItem
			if err := tx.Where("group_id = ?", groupID).Find(&existing).Error; err != nil {
				return fmt.Errorf("failed to load group items: %w", err)
			}
			hasGrant := make(map[int]bool, len(existing))
			maxPriority := 0
			for _, item := range existing {
				hasGrant[item.ChannelGrantID] = true
				if item.Priority > maxPriority {
					maxPriority = item.Priority
				}
			}
			for _, grantID := range grantIDs {
				if hasGrant[grantID] {
					result.Skipped = append(result.Skipped, grantID)
					continue
				}
				// 主键在自增: 同一事务内的两次 Create 各自拿到不同 ID, 无需客户端指定。
				item := model.GroupItem{GroupID: groupID, ChannelGrantID: grantID, Priority: maxPriority + 1}
				if err := tx.Create(&item).Error; err != nil {
					return fmt.Errorf("failed to create group item: %w", err)
				}
				maxPriority++
				hasGrant[grantID] = true
				result.Added = append(result.Added, grantID)
			}
			return tx.Preload("Items").First(&group, groupID).Error
		})
		if err != nil {
			// 多分组提交时前面几个分组可能已经写成功: 返回已完成的那些, 让界面能如实展示"部分成功",
			// 而不是整体失败后让用户重复提交(虽然本函数幂等, 但重复点击终究是坏体验)。
			return results, fmt.Errorf("failed to add group items: %w", err)
		}
		sortGroupItems(group.Items)
		groupCache.Set(group.ID, group)
		result.Group = groupSnapshot(group, scope)
		results = append(results, result)
		updated = append(updated, group)
	}
	return results, nil
}

// GroupDel 删除分组及其成员，成员删除不会影响被其他分组引用的渠道授权。
func GroupDel(id int, scope model.Scope, ctx context.Context) error {
	group, ok := groupCache.Get(id)
	if !ok || group.UserID != scope.ID {
		return fmt.Errorf("group not found")
	}
	if err := db.GetDB().WithContext(ctx).Delete(&model.Group{}, id).Error; err != nil {
		return fmt.Errorf("failed to delete group: %w", err)
	}
	groupCache.Del(id)
	groupNameIndex.Del(groupIndexKey(group.UserID, group.Name))
	return nil
}

// groupRefreshCache 从数据库刷新完整分组缓存和名称索引。
// 缓存只存库内行, 成员的名称与可用性在读取时由 groupSnapshot 现算: 它们随渠道与凭据变化,
// 存进缓存就得在每次渠道改动后跟着刷新一遍。
func groupRefreshCache(ctx context.Context) error {
	groups := []model.Group{}
	if err := db.GetDB().WithContext(ctx).
		Preload("Items").
		Find(&groups).Error; err != nil {
		return err
	}
	groupCache.Clear()
	groupNameIndex.Clear()
	for _, group := range groups {
		model.NormalizeGroupRelayConfigForMode(&group.RelayConfig, group.Mode)
		sortGroupItems(group.Items)
		groupCache.Set(group.ID, group)
		groupNameIndex.Set(groupIndexKey(group.UserID, group.Name), group.ID)
	}
	return nil
}

// sortGroupItems 按优先级和主键生成稳定的成员顺序。
func sortGroupItems(items []model.GroupItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Priority != items[j].Priority {
			return items[i].Priority < items[j].Priority
		}
		return items[i].ID < items[j].ID
	})
}

// groupSnapshot 为成员补齐授权两侧的名称, 所属渠道与可用性。
// 供给侧隔离在此一次定稿: 只有「已发布渠道 + 已上架模型」的成员进入展示与选路, 对所有角色一视同仁
// (含渠道归属者与管理员); 未上架模型只在归属者的渠道管理页存在, 不出模型配置、不参与转发。
// 可用性同此一次定稿: 渠道与凭据均启用且模型, 凭据均存在时可转发, 否则仍列出该成员但标记不可用,
// 由此界面无需再按渠道列表回查, 也不会出现前后端各判一套的分歧。
func groupSnapshot(group model.Group, scope model.Scope) model.Group {
	// 成员恒为数组: 读取侧承诺该字段不为 null, 空分组也要给出空数组。
	items := make([]model.GroupItem, 0, len(group.Items))
	for _, item := range group.Items {
		grant, ok := channelGrantCache.Get(item.ChannelGrantID)
		if !ok {
			continue
		}
		item.Protocols = grant.Protocols
		channelModel, modelOK := channelModelCache.Get(grant.ChannelModelID)
		channelKey, keyOK := channelKeyCache.Get(grant.ChannelKeyID)
		if !modelOK || !keyOK {
			continue
		}
		channel, channelOK := channelCache.Get(channelModel.ChannelID)
		if !channelOK {
			continue
		}
		// 供给侧隔离: 未发布渠道或未上架模型的成员直接不出快照, 界面与选路同此口径。
		// (只查 shared+listed 而不查审核位是既有语义: 复审导致 draft/stale 不应让存量成员消失。)
		if !channel.Shared || !channelModel.Listed {
			continue
		}
		item.ChannelID = channelModel.ChannelID
		item.ChannelKeyID = grant.ChannelKeyID
		item.ChannelModelID = grant.ChannelModelID
		item.ModelName = channelModel.Name
		item.KeyName = channelKey.Name
		// 多币种: 渠道模型可按 CNY/EUR 等报价, 而分组成员价格是选路(价格优先/评分)与
		// reserveBalance 预扣的唯一来源。折算与上浮统一走 grantLedgerPrices(见 internal/op/grant_view.go),
		// 否则一个 CNY 报价的成员会被当成比 USD 成员便宜约 7 倍, 既吃掉全部流量,
		// 又按约 1/7 的价格预扣与结算。牌价不可用时价格清零并标记 FxMissing + Available=false,
		// 选路跳过但界面仍列出并给出原因。
		parts := grantParts{Channel: channel, ChannelKey: channelKey, ChannelModel: channelModel}
		supplyPrice, mediaSupply, priceCurrency, userPrice, userMedia, fxOK := grantLedgerPrices(parts)
		item.PriceCurrency = priceCurrency
		item.FxMissing = !fxOK
		item.SupplyPrice = supplyPrice
		item.UserPrice = userPrice
		// 媒体定价与形态; 文本模型上这两个字段为零值, 不影响现有 JSON 形状。
		item.MediaSupply = mediaSupply
		item.UserMedia = userMedia
		item.Kind = channelModel.Kind
		// 展示名与可用性判定同候选口径(单点见 grant_view.go): 已发布渠道一律显示唯一编码(#1001)。
		item.ChannelName = grantDisplayName(channel)
		item.Available = grantAvailability(parts, fxOK)
		if !scope.Owns(channel.UserID) {
			item.KeyName = ""
		}
		items = append(items, item)
	}
	group.Items = items
	return group
}
