# message-catalog-go

本地化消息目录的加载、校验、伪本地化与查询库。整条链路只在本进程内运行：
只读取本地目录与内存数据，不依赖任何外部服务与真实翻译平台。

```go
c := catalog.New(catalog.Config{
    BaseLocale: "en",
    Fallback: map[string][]string{
        "fr-CA": {"fr", "en"},   // 多级回退链
        "fr":    {"en"},
    },
    PseudoEnabled: true,
    MaxEntries:    100000,
})
issues, err := c.Load("./messages")   // messages/<lang>.json
out, res, err := c.Render("fr-CA", "cart.items", map[string]any{"n": 3})
// res.ActualLang 告诉你实际命中了哪种语言
```

## 目录格式

`dir/<lang>.json`，内容为 JSON 对象 `{ "key": "message" }`。
消息支持 ICU MessageFormat 子集：

- 占位符：`{name}`、`{count, number}`（另有 `date`/`time`）
- 复数：`{n, plural, offset:1 =0{...} one{...} other{...}}`，分支体内 `#` 表示数值
- 选择/性别：`{g, select, male{...} female{...} other{...}}`
- 嵌套：分支体内可再含占位符/复数/选择
- 转义：`''` → `'`，`'{` → `{`，`'}` → `}`，`'#` → `#`

## 回退语义

- 每种语言的回退链为 `[自身] + Fallback[lang]`，可多级；链中重复语言自动去重（防环）。
- 查询沿链取**第一个**命中的条目，命中唯一。
- 每次解析返回 `Resolution{WantLang, ActualLang, Chain, Found}`，可解释最终命中哪条记录、走过了哪些语言。
- 整条链都未命中返回 `ErrNotFound`，`Resolution.Chain` 仍完整记录。
- 某语言缺失条目不是校验错误，由回退链在查询时兜底。

## 校验

`Load`/`Rebuild` 返回 `[]Issue`，单条错误不会让整批加载失败。
以 `BaseLocale` 为参照，对每种语言的同名条目比对签名，错误定位到具体语言与条目，
原因码可区分：

| Reason | 含义 |
|---|---|
| `parse_error` | 模板语法错误 |
| `placeholder_name_mismatch` | 占位符名称与基准不一致 |
| `placeholder_count_mismatch` | 同名占位符出现次数不一致 |
| `plural_category_mismatch` | 复数分支类别集不一致（含 `=N`） |
| `select_category_mismatch` | 选择分支类别集不一致 |
| `missing_other_branch` | 复数/选择缺少必需的 `other` 分支 |
| `limit_exceeded` | 超出配置的规模上限 |

## 伪本地化

`PseudoEnabled: true` 时，每种语言额外生成 `<lang>-pseudo` 伪本地化产物，规则确定：

1. 可翻译文本中的元音扩张为两个带重音字符（`a→áà`，约 +40% 长度）；
2. 整条消息用 `⟦ ⟧` 包裹作为标记；
3. 占位符、复数/选择分支结构、转义字符、复数分支中的 `#` 一律不变。

伪本地化产物直接渲染即得最终结果，与服务端渲染路径一致；
占位符替换值不会被伪本地化，渲染后不会变形或被吞掉。

## 并发与版本

- `Catalog` 内部用原子指针切换不可变的 `Snapshot`：任一时刻查询到的都是某个已提交的完整版本，不会出现语言级或条目级的混合状态。
- 多次查询需要同一版本视图时使用 `v := c.View()`，同一 `View` 上的查询保证版本一致。
- 查询不加锁，慢查询（持有旧视图）不阻塞 `Load`/`Rebuild` 推进。

## 增量重建

`Rebuild(dir)` 按文件内容哈希对比，只重新解析新增/修改/删除的文件，
未变化文件的解析结果直接复用；产出与全量 `Load` 完全一致，
并返回 `Affected{Langs, Keys}` 说明哪些语言与条目被判定为受影响。

## 崩溃恢复

`Save(dir)` 用双槽位 + `CURRENT` 指针原子落盘（tmp 写入 + fsync + rename）。
加载或落盘在任意点中断都不会留下不可用状态：
重启后 `Restore(dir)` 要么完整沿用上一个版本，要么完整使用新版本；
`CURRENT` 指向的槽位损坏时自动回退到另一个完整槽位。

## 配置项

| 字段 | 含义 | 超限/变更行为 |
|---|---|---|
| `BaseLocale` | 校验参照语言 | 变更只影响后续 Load/Rebuild |
| `Fallback` | 每种语言的多级回退链 | 同上 |
| `PseudoEnabled` | 是否生成 `<lang>-pseudo` | 同上 |
| `MaxEntries` | 目录条目总数上限（0 不限） | 超限时整批拒绝（`ErrLimitExceeded`），旧版本保持不变，行为确定可复现 |
| `MaxFileBytes` | 单个目录文件大小上限（0 不限） | 超限文件记 `limit_exceeded` Issue 并跳过，其余正常加载 |

## 测试

```sh
go test ./...          # 全部测试
go test ./... -race    # 含并发查询/重建竞争检测
```

覆盖：回退取值（多级、唯一命中、可解释）、占位符/复数/选择校验、
伪本地化（结构保持、渲染一致、确定性）、增量重建（与全量一致、受影响说明）、
重启恢复（槽位损坏回退、中断无残留）、并发查询（版本一致、慢查询不阻塞重建）、
大规模目录（2 万条约 0.2s 加载、70 万 QPS 查询）。
