// Package catalog implements loading, merging, validation, pseudo-localization
// and crash-safe versioned publishing of localized message catalogs.
package catalog

// Node is a single node of a parsed ICU-like message.
type Node interface {
	node()
}

// Text is a literal text run. Quoted (escaped) literal text is also Text;
// Quoted marks text that was wrapped in ' quotes (so '#' and braces inside it
// are literal and must not be interpreted as syntax).
type Text struct {
	Value  string
	Quoted bool
}

// Placeholder is a simple {name} argument.
type Placeholder struct {
	Name string
}

// Plural is a {name, plural, ...} branch node.
type Plural struct {
	Name     string
	Offset   int
	Branches []Branch
}

// Select is a {name, select, ...} branch node.
type Select struct {
	Name     string
	Branches []Branch
}

// Gender is a {name, gender, ...} branch node. It behaves like Select but is
// restricted to the male/female/other categories.
type Gender struct {
	Name     string
	Branches []Branch
}

// Branch is one arm of a Plural/Select/Gender node.
type Branch struct {
	// Key is the selector value, e.g. "one", "other", "=0", "male".
	Key string
	// Exact is true when Key is an explicit "=n" match (plural only).
	Exact bool
	// Value is the message rendered when this branch matches.
	Value []Node
}

func (*Text) node()        {}
func (*Placeholder) node() {}
func (*Plural) node()      {}
func (*Select) node()      {}
func (*Gender) node()      {}

// Pos is a source position inside a raw message string.
type Pos struct {
	Line int
	Col  int
}
