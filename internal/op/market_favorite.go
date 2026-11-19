package op

import (
	"errors"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// MarketFavoriteList 返回该用户收藏的模型名, 按收藏时间升序(先收藏的在前)。
func MarketFavoriteList(userID uint) ([]string, error) {
	var rows []model.MarketFavorite
	if err := db.GetDB().Where("user_id = ?", userID).Order("created_at asc, id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Model)
	}
	return names, nil
}

// MarketFavoriteAdd 收藏一个模型名。重复收藏幂等(不报错、不重复插入)。
//
// 只校验形状不校验存在性: 模型名当下来自广场, 但广场数据会随渠道上下架变化。
// 若这里要求"必须此刻在售", 用户收藏的模型一停用就会连收藏都写不进去 —— 而收藏的意义恰恰是
// "等它恢复"。展示侧按当前目录核对存在性即可。
func MarketFavoriteAdd(userID uint, name string) error {
	name, err := marketFavoriteName(name)
	if err != nil {
		return err
	}
	// 幂等靠唯一索引兜底而不是先查后插: 两个标签页同时点收藏时, 先查后插会双双通过检查。
	err = db.GetDB().Where(model.MarketFavorite{UserID: userID, Model: name}).FirstOrCreate(&model.MarketFavorite{}).Error
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
		return nil // 并发插入撞唯一索引: 结果与"已经在收藏里"一致。
	}
	return err
}

// MarketFavoriteRemove 取消收藏。取消一个本就没收藏的模型不报错(幂等)。
func MarketFavoriteRemove(userID uint, name string) error {
	name, err := marketFavoriteName(name)
	if err != nil {
		return err
	}
	return db.GetDB().Where("user_id = ? AND model = ?", userID, name).Delete(&model.MarketFavorite{}).Error
}

// marketFavoriteName 归一化并校验模型名。
//
// 模型名是对用户可见的上游标识, 长度上限与 channel_models.name 的 128 对齐; 去掉首尾空白是因为
// 前端把用户输入直接传上来时很容易带上空格, 而带空格的收藏在广场按名匹配时永远匹配不到 ——
// 表现就是"收藏了但筛选后看不到", 极难排查。
func marketFavoriteName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("model name is required")
	}
	if len(name) > 128 {
		return "", errors.New("model name is too long")
	}
	return name, nil
}