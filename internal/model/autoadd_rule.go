package model

// ModelAutoAddRule 是「某个用户的某个模型」自己的自动添加条件。
//
// 全局条件(channel_auto_add_* 设置键)由管理员统一设置, 对所有人所有模型一视同仁;
// 但同一个模型在不同用户眼里值得添加与否往往不一样 —— 有人只收低价, 有人只收旗舰。
// 本表让每人对每个模型各设一份条件, 全局值降级为「此人没单独设置该模型时的默认」。
//
// 寻址是 (user_id, name): 每个用户看到的都是自己设的条件, 互不干扰;
// 管理员可以在别人的视角下代为设置(scope 决定写谁的), 不必切换登录。
//
// 为什么是独立表而不是 llm_infos 加列: llm_infos 由 models.dev 价格同步整行覆盖
// (LLMBatchSave 用 OnConflict UpdateAll), 挂在那里的条件会在每次价格刷新时被抹回零值,
// 管理员设好的条件静默失效 —— 这是那种「没有报错、只有条件突然不管用」的坑。
// 独立表让价格同步完全碰不到条件, 两份数据的生命周期天然分开。
//
// 全部条件都是字符串: 空串 = 未单独设置, 回落全局默认; 非空 = 覆盖。
// 用字符串而不是指针/三态布尔, 是为让 JSON 往返「没填」与「填了空」天然等价,
// 前端一个输入框就能同时承担展示与提交, 无需区分 null 与 0。
type ModelAutoAddRule struct {
	UserID uint   `json:"user_id" gorm:"primaryKey;not null"` // 条件归属者。
	Name   string `json:"name" gorm:"primaryKey;not null"`    // 模型名(小写规范化)。

	// 三态开关: ""(跟随全局/渠道开关) | "on"(该模型强制并入, 跳过其余条件) | "off"(该模型永不并入)。
	// 值域在 Normalize 里收紧, 非法值在保存时即被拒, 不留到运行期才降级。
	Mode string `json:"mode" gorm:"size:8;not null;default:''"` // on | off | 空。

	PriceMin  string `json:"price_min" gorm:"size:32;not null;default:''"`  // 参考价下限, 空=回落全局。
	PriceMax  string `json:"price_max" gorm:"size:32;not null;default:''"`  // 参考价上限, 空=回落全局。
	MarkupMin string `json:"markup_min" gorm:"size:32;not null;default:''"` // 倍率下限, 空=回落全局。
	MarkupMax string `json:"markup_max" gorm:"size:32;not null;default:''"` // 倍率上限, 空=回落全局。
	Exclude   string `json:"exclude" gorm:"size:256;not null;default:''"`   // 排除正则, 空=回落全局。
	Include   string `json:"include" gorm:"size:256;not null;default:''"`   // 白名单正则, 空=回落全局。
	FillPrice string `json:"fill_price" gorm:"size:8;not null;default:''"`  // on | off | 空(回落全局)。
}

// 模型级条件的三态取值。
const (
	AutoAddRuleModeOn  = "on"
	AutoAddRuleModeOff = "off"
)
