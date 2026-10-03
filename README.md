# message-catalog-go

本地化消息目录的校验与伪本地化（pseudo-localization）工具库。整条链路只在
本进程内运行，只读取本地目录与内存数据，不依赖任何外部服务或真实翻译平台。

- 多语言目录加载与多级回退（fallback）合并
- 占位符 / 复数 / 选择 / 性别分支语法校验，错误定位到具体条目并给出可区分原因
- 伪本地化产物生成，占位符、转义字符与结构不变，产物可被同一渲染器渲染
- 目录文件新增 / 修改 / 删除后的增量重建，结果与全量重建完全一致
- 加载 / 重建与查询并发：查询永远看到某个已提交的完整版本，无语言级 / 条目级混合状态
- 崩溃安全：落盘中断后重启要么完整沿用旧版本，要么完整使用新版本
- 回退链、伪本地化开关、目录规模上限等配置，配置变更只影响后续处理

## 消息格式

每个语言一个 JSON 文件（`<locale>.json`），内容为扁平的 `键 -> 消息` 映射。
消息语法为 ICU MessageFormat 的子集：

```
{name}                                   简单占位符
{count, plural, one {# 个} other {# 个}}  复数（plural）
{g, select, male {他} female {她} other {他们}}  选择（select）
{g, gender, male {他} female {她} other {他们}}  性别（gender）
{n, plural, offset:2 =0 {免费} one {# 天} other {# 天}}  带偏移的复数
'{name}'   转义：输出字面量 {name}（不替换占位符）
''         字面量撇号
```

规则：

- 复数分支必须包含 `other`；类别名只能是 `zero/one/two/few/many/other` 或
  `=数字` 的精确匹配。
- 选择 / 性别分支必须包含 `other`；性别分支只能是 `male/female/other`。
- 复数中 `#` 渲染为减去 `offset` 后的数值；`=n` 精确匹配**原始**数值。
- 单条消息解析失败不会中断整批加载：该条目带错误报告，但不影响其他条目。

## 回退语义

每个语言的回退链由配置决定，解析顺序为：

```
<语言自身> -> 配置的该语言链 -> 默认链（DefaultChain）
```

- 链可以有多级，例如 `zh_TW -> zh -> en`。
- 命中规则：按链顺序取第一个**存在该键**的语言条目；一旦命中立即停止。
- 最终命中哪一条记录是唯一且可解释的：`Resolution.Chain` 记录每一步的
  语言、是否命中（`hit` / `miss`）及原因（`key missing` / `locale not loaded`），
  `SourceLocale` 给出最终取值语言。
- 整条链都找不到键时，该语言的该键无解析结果，并报告 `MISSING_TRANSLATION`。

## 校验错误码

| Code | 含义 |
| --- | --- |
| `PARSE` | 消息语法错误（括号未闭合、引号未闭合等） |
| `MISSING_OTHER` | 复数 / 选择 / 性别分支缺少必需的 `other` 分支 |
| `INVALID_PLURAL_CATEGORY` | 复数类别名非法 |
| `INVALID_GENDER_CATEGORY` | 性别类别名非法 |
| `DUPLICATE_BRANCH` | 分支键重复 |
| `PLACEHOLDER_MISMATCH` | 同键跨语言的占位符名称集合不一致 |
| `BRANCH_KIND_MISMATCH` | 同键跨语言的分支节点类型（plural/select/gender）不一致 |
| `MISSING_TRANSLATION` | 回退链上找不到该键 |

跨语言一致性校验只比较占位符名称集合与分支节点类型，不比较复数类别集合
（不同语言的 CLDR 复数类别本就不同，例如中文没有 `one`）。

## 伪本地化规则

伪本地化在**不改变占位符、转义字符与整体结构**的前提下，对可翻译文本做
扩张与标记：

- 普通文本：ASCII 字母加变音符号（如 `Hello` -> `Ĥéĺĺö`），按长度约 1/3 扩张，
  并在两端加上 `[!! ... !!]` 标记，便于发现截断与硬编码字符串问题。
- 占位符 `{name}`：原样复制，名称不变。
- 复数 / 选择 / 性别节点：节点类型、变量名、`offset`、分支键全部原样保留，
  仅递归处理分支内文本；`#` 不在 AST 中，渲染时仍正常替换为数值。
- 转义文本（引号内的字面量，如 `'{name}'`）：原样保留，不做变形。
- 生成的伪消息可被同一个 `Render` 函数渲染，占位符不会变形或被吞掉。

伪本地化产物写入一个额外的伪语言（默认 `en_XA`），可直接导出为
`<pseudo-locale>.json` 文件。

## 增量重建

- 构建时记录每个源文件的大小、修改时间与 SHA-256。
- 重建时只重新解析新增 / 修改的文件；未修改文件直接复用上一版本的条目。
- 受影响的键包括：变更文件中的键、变更语言的全部键、以及回退链经过变更
  语言的其他语言的键（覆盖新增键与删除键）。
- 重建会清理已不存在于任何语言的键的陈旧解析。
- 增量重建的结果与对同一目录做全量重建的结果**逐键一致**（测试覆盖）。
- 报告 `FilesAdded/FilesModified/FilesDeleted` 与 `Affected`（语言 -> 键列表）
  说明哪些文件、语言、条目被判定为受影响。

## 并发模型

- 每个版本是不可变快照（`Version`），通过 `atomic.Pointer` 原子发布。
- 查询（`Store.Current()` / `Version.Query`）无锁、不阻塞，永远看到某个
  已提交的完整版本，不会出现语言级或条目级的混合状态。
- 重建在独立副本上进行，完成后原子切换指针；重建期间查询继续使用旧版本。
- 慢查询持有旧版本引用不影响重建推进；旧版本在被引用期间保持完整可读。
- 重建本身串行化（`Store.Update` 内部加锁），避免并发重建互相覆盖。

## 崩溃安全

快照写入采用临时文件 + fsync + 原子 rename：

1. 内容写入 `snapshot.json.tmp`，fsync；
2. 校验和写入 `snapshot.sha256.tmp`，fsync；
3. 当前文件改名为 `snapshot.prev.json`，临时文件改名为 `snapshot.json`；
4. fsync 目录。

任意时刻崩溃：

- 重命名前崩溃：当前快照仍是旧版本（临时文件被忽略）；
- 重命名之间崩溃：当前快照缺失或校验不匹配，启动时回退到 `prev` 快照；
- 首次保存也会保留一份 `prev`，因此即使第一次保存后文件损坏也能回退。

启动时校验 SHA-256，当前快照不可用则回退到上一份，两者都不可用则返回
`ErrNoSnapshot`，由调用方重新全量构建。

## 配置项

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `LocalesDir` | - | 存放 `<locale>.json` 的目录 |
| `Fallback` | `{}` | 语言 -> 有序回退链（不含自身） |
| `DefaultChain` | `["en"]` | 未在 `Fallback` 中配置的语言使用的默认链 |
| `Pseudo.Enabled` | `false` | 是否生成伪语言 |
| `Pseudo.Locale` | `en_XA` | 伪语言代码 |
| `Pseudo.SourceLocale` | `en` | 伪本地化的源语言 |
| `MaxEntries` | `1000000` | 全部语言条目总数上限，超出返回 `ErrCatalogTooLarge`，不发布新版本 |
| `MaxFileSize` | `64MiB` | 单文件大小上限 |

配置变更只影响后续重建：已发布的版本保留其构建时的配置，旧版本的回退
行为不发生变化。

## CLI

```bash
# 校验目录（打印错误并以非零退出码退出）
msgcat validate --fallback zh_TW=zh,en --fallback zh=en ./locales

# 全量构建并保存崩溃安全快照
msgcat build --fallback zh_TW=zh,en ./locales ./snapshot

# 查询（解析 + 渲染，显示回退链）
msgcat query ./snapshot zh_TW greeting --set name=小明

# 生成伪语言产物
msgcat pseudo --pseudo ./locales ./out
```

## 编程接口

```go
store := catalog.NewStore()
v, report, err := store.Update(ctx, cfg)          // 全量或增量重建
out, resolution, err := v.Query("zh_TW", "greeting", map[string]any{"name": "小明"})
// resolution.SourceLocale / resolution.Chain 可解释唯一命中来源
err = store.SaveSnapshot("./snapshot")           // 崩溃安全落盘
v2, report, err := store.LoadSnapshot("./snapshot") // 重启恢复
```
