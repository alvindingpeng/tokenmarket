package op

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/cache"
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

// validateGroupItemAccess 校验分组成员可用性: 一律只接受「已发布渠道 + 已上架模型」的授权,
// 对所有角色一视同仁(含渠道归属者与管理员), 与快照的供给侧隔离同口径。
func validateGroupItemAccess(grantID int, scope model.Scope) error {
	grant, ok := channelGrantCache.Get(grantID)
	if !ok {
		return fmt.Errorf("channel grant not found")
	}
	channelModel, ok := channelModelCache.Get(grant.ChannelModelID)
	if !ok {
		return fmt.Errorf("channel model not found")
	}
	channel, ok := channelCache.Get(channelModel.ChannelID)
	if !ok {
		return fmt.Errorf("channel not found")
	}
	if channel.Shared && channelModel.Listed {
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
	markup := SettingGetFloat(model.SettingKeyMarkupRatio)
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
		if !channel.Shared || !channelModel.Listed {
			continue
		}
		item.ChannelID = channelModel.ChannelID
		item.ChannelKeyID = grant.ChannelKeyID
		item.ChannelModelID = grant.ChannelModelID
		item.ModelName = channelModel.Name
		item.KeyName = channelKey.Name
		item.SupplyPrice = channelModel.SupplyPrice
		item.UserPrice = channelModel.SupplyPrice.Scale(markup)
		// 媒体定价与形态; 文本模型上这两个字段为零值, 不影响现有 JSON 形状。
		item.MediaSupply = channelModel.MediaSupply
		item.UserMedia = channelModel.MediaSupply.Scale(markup)
		item.Kind = channelModel.Kind
		item.ChannelName = channel.Name
		item.Available = channel.Enabled && channelKey.Enabled
		// 展示名定稿: 已发布渠道一律显示发布唯一编码, 上游名称不出分组管理; 凭据名按归属脱敏。
		if channel.ShareCode != "" {
			item.ChannelName = channel.ShareCode
		}
		if !scope.Owns(channel.UserID) {
			item.KeyName = ""
		}
		items = append(items, item)
	}
	group.Items = items
	return group
}
