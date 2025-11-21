package op

import (
	"context"
	"fmt"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/utils/cache"
)

var groupCache = cache.New[int, model.Group](16)

func GroupList(ctx context.Context) ([]model.Group, error) {
	groups := make([]model.Group, 0, groupCache.Len())
	for _, group := range groupCache.GetAll() {
		groups = append(groups, group)
	}
	return groups, nil
}

func GroupListModel(ctx context.Context) ([]string, error) {
	modelSet := make(map[string]bool)
	for _, group := range groupCache.GetAll() {
		if group.ModelName != "" {
			modelSet[group.ModelName] = true
		}
	}

	models := []string{}
	for model := range modelSet {
		models = append(models, model)
	}

	return models, nil
}

func GroupCreate(group *model.Group, ctx context.Context) error {
	if err := db.GetDB().WithContext(ctx).Create(group).Error; err != nil {
		return err
	}
	groupCache.Set(group.ID, *group)
	return nil
}

func GroupUpdate(group *model.Group, ctx context.Context) error {
	oldGroup, ok := groupCache.Get(group.ID)
	if !ok {
		return fmt.Errorf("group not found")
	}
	if oldGroup == *group {
		return nil
	}
	if err := db.GetDB().WithContext(ctx).Save(group).Error; err != nil {
		return err
	}
	groupCache.Set(group.ID, *group)
	return nil
}

func GroupDel(id int, ctx context.Context) error {
	group, ok := groupCache.Get(id)
	if !ok {
		return fmt.Errorf("group not found")
	}
	if err := db.GetDB().WithContext(ctx).Delete(&group).Error; err != nil {
		return err
	}
	groupCache.Del(group.ID)
	return nil
}

func groupRefreshCache(ctx context.Context) error {
	groups := []model.Group{}
	if err := db.GetDB().WithContext(ctx).Find(&groups).Error; err != nil {
		return err
	}
	for _, group := range groups {
		groupCache.Set(group.ID, group)
	}
	return nil
}
