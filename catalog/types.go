// Package catalog 提供本地化消息目录的加载、校验、伪本地化与查询能力。
package catalog

// Node 是消息模板 AST 的节点接口。
type Node interface{ node() }

// Text 普通文本片段。
type Text struct{ Value string }

// Arg 简单占位符，如 {name} 或 {count, number}。
type Arg struct {
	Name string
	Type string // "", "number", "date", "time"
}

// Plural 复数分支，如 {n, plural, one{...} other{...}}。
type Plural struct {
	Arg      string
	Offset   int
	Exacts   map[int][]Node    // =0、=1 等精确匹配
	Variants map[string][]Node // one/other 等类别
}

// Select 选择/性别分支，如 {g, select, male{...} other{...}}。
type Select struct {
	Arg      string
	Variants map[string][]Node
}

func (Text) node()   {}
func (Arg) node()    {}
func (Plural) node() {}
func (Select) node() {}

// Message 一条已解析的消息。
type Message struct {
	Key   string
	Lang  string
	Raw   string
	Nodes []Node
}

// Entry 目录中的一条记录（未解析或已解析）。
type Entry struct {
	Key string
	Raw string
}

// Reason 校验错误原因码，可区分。
type Reason string

const (
	ReasonParseError       Reason = "parse_error"
	ReasonPlaceholderName  Reason = "placeholder_name_mismatch"
	ReasonPlaceholderCount Reason = "placeholder_count_mismatch"
	ReasonPluralCategory   Reason = "plural_category_mismatch"
	ReasonMissingOther     Reason = "missing_other_branch"
	ReasonSelectCategory   Reason = "select_category_mismatch"
	ReasonLimitExceeded    Reason = "limit_exceeded"
)

// Issue 单条校验错误，定位到具体语言与条目。
type Issue struct {
	Lang   string
	Key    string
	Reason Reason
	Detail string
}

func (i Issue) Error() string {
	return i.Lang + ":" + i.Key + ": " + string(i.Reason) + ": " + i.Detail
}

// Config 目录配置。
type Config struct {
	BaseLocale    string              // 基准语言，校验参照
	Fallback      map[string][]string // 每种语言的回退链（多级）
	PseudoEnabled bool
	MaxEntries    int   // 目录条目总数上限，0 表示不限制
	MaxFileBytes  int64 // 单个目录文件大小上限，0 表示不限制
}

// Affected 描述一次增量重建受影响的语言与条目。
type Affected struct {
	Langs []string
	Keys  []string
}

// Resolution 一次查询的解析结果，可解释回退命中。
type Resolution struct {
	Key        string
	WantLang   string   // 请求的语言
	ActualLang string   // 实际命中的语言
	Chain      []string // 实际走过的回退链
	Found      bool
}
