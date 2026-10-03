package catalog

import (
	"fmt"
	"strings"
)

// ParseError is a syntax error found while parsing a raw message.
type ParseError struct {
	Pos     Pos
	Message string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("line %d, col %d: %s", e.Pos.Line, e.Pos.Col, e.Message)
}

// Parse parses an ICU-like message into an AST.
//
// Supported subset:
//   - literal text with ' quoting ('' is a literal apostrophe)
//   - {name}
//   - {name, plural, offset:n <selector> {msg} ...}
//   - {name, select, <selector> {msg} ...}
//   - {name, gender, <selector> {msg} ...}
//
// The parser is strict: a stray '}' or an unterminated quote is an error.
func Parse(raw string) ([]Node, error) {
	p := &parser{raw: raw}
	nodes, err := p.parseMessage(false)
	if err != nil {
		return nil, err
	}
	return nodes, nil
}

type parser struct {
	raw string
	pos int
}

func (p *parser) cur() byte {
	if p.pos >= len(p.raw) {
		return 0
	}
	return p.raw[p.pos]
}

func (p *parser) posAt(off int) Pos {
	line, col := 1, 1
	for i := 0; i < off && i < len(p.raw); i++ {
		if p.raw[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return Pos{Line: line, Col: col}
}

func (p *parser) failf(format string, args ...any) error {
	return &ParseError{Pos: p.posAt(p.pos), Message: fmt.Sprintf(format, args...)}
}

func (p *parser) skipSpaces() {
	for p.pos < len(p.raw) {
		switch p.raw[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

// parseMessage parses segments until end of input or, when stopAtBrace is true,
// a closing '}' that belongs to the enclosing branch.
func (p *parser) parseMessage(stopAtBrace bool) ([]Node, error) {
	var nodes []Node
	for p.pos < len(p.raw) {
		switch p.cur() {
		case '}':
			if stopAtBrace {
				return nodes, nil
			}
			return nil, p.failf("unexpected '}'")
		case '{':
			n, err := p.parsePlaceholder()
			if err != nil {
				return nil, err
			}
			nodes = append(nodes, n)
		case '\'':
			if p.pos+1 < len(p.raw) && p.raw[p.pos+1] == '\'' {
				// '' is always a literal apostrophe.
				nodes = append(nodes, &Text{Value: "'"})
				p.pos += 2
				continue
			}
			if p.pos+1 < len(p.raw) && (p.raw[p.pos+1] == '{' || p.raw[p.pos+1] == '}' || p.raw[p.pos+1] == '#') {
				// A quote immediately before a syntax character opens a
				// quoted literal.
				text, err := p.parseQuoted()
				if err != nil {
					return nil, err
				}
				if text != "" {
					nodes = append(nodes, &Text{Value: text, Quoted: true})
				}
				continue
			}
			// Otherwise the apostrophe is literal.
			nodes = append(nodes, &Text{Value: "'"})
			p.pos++
		default:
			text := p.parseTextRun(stopAtBrace)
			if text != "" {
				nodes = append(nodes, &Text{Value: text})
			}
		}
	}
	if stopAtBrace {
		return nil, p.failf("unterminated branch, expected '}'")
	}
	return nodes, nil
}

// parseTextRun reads literal text until a syntax character.
func (p *parser) parseTextRun(stopAtBrace bool) string {
	start := p.pos
	for p.pos < len(p.raw) {
		c := p.raw[p.pos]
		if c == '{' || c == '\'' || c == '}' {
			if c == '}' && !stopAtBrace {
				break
			}
			if c == '}' {
				break
			}
			break
		}
		p.pos++
	}
	return p.raw[start:p.pos]
}

// parseQuoted reads a '-quoted literal. '' is a literal apostrophe.
func (p *parser) parseQuoted() (string, error) {
	p.pos++ // opening quote
	var sb strings.Builder
	for p.pos < len(p.raw) {
		c := p.raw[p.pos]
		if c == '\'' {
			if p.pos+1 < len(p.raw) && p.raw[p.pos+1] == '\'' {
				sb.WriteByte('\'')
				p.pos += 2
				continue
			}
			p.pos++ // closing quote
			return sb.String(), nil
		}
		sb.WriteByte(c)
		p.pos++
	}
	return "", p.failf("unterminated quoted literal, expected '")
}

// parsePlaceholder parses a '{' ... '}' argument.
func (p *parser) parsePlaceholder() (Node, error) {
	p.pos++ // consume '{'
	p.skipSpaces()
	name, err := p.readName()
	if err != nil {
		return nil, err
	}
	p.skipSpaces()
	if p.cur() == '}' {
		p.pos++
		return &Placeholder{Name: name}, nil
	}
	if p.cur() != ',' {
		return nil, p.failf("expected ',' or '}' after argument name")
	}
	p.pos++ // consume ','
	p.skipSpaces()
	typ, err := p.readName()
	if err != nil {
		return nil, p.failf("expected argument type (plural, select, gender)")
	}
	switch typ {
	case "plural":
		return p.parsePlural(name)
	case "select", "gender":
		return p.parseSelectLike(name, typ)
	default:
		return nil, p.failf("unknown argument type %q", typ)
	}
}

func (p *parser) readName() (string, error) {
	start := p.pos
	for p.pos < len(p.raw) {
		c := p.raw[p.pos]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
			p.pos++
		} else {
			break
		}
	}
	if start == p.pos {
		return "", p.failf("expected argument name")
	}
	return p.raw[start:p.pos], nil
}

// expectComma consumes a comma between type and style.
func (p *parser) expectComma() error {
	p.skipSpaces()
	if p.cur() != ',' {
		return p.failf("expected ',' before argument style")
	}
	p.pos++
	p.skipSpaces()
	return nil
}

func (p *parser) parsePlural(name string) (Node, error) {
	if err := p.expectComma(); err != nil {
		return nil, err
	}
	offset := 0
	if p.matchWord("offset") {
		p.skipSpaces()
		if p.cur() != ':' {
			return nil, p.failf("expected ':' after offset")
		}
		p.pos++
		p.skipSpaces()
		n, err := p.readNumber()
		if err != nil {
			return nil, err
		}
		offset = n
		p.skipSpaces()
	}
	branches, err := p.parseBranches()
	if err != nil {
		return nil, err
	}
	if p.cur() != '}' {
		return nil, p.failf("expected '}' closing plural argument")
	}
	p.pos++
	return &Plural{Name: name, Offset: offset, Branches: branches}, nil
}

func (p *parser) parseSelectLike(name, typ string) (Node, error) {
	if err := p.expectComma(); err != nil {
		return nil, err
	}
	branches, err := p.parseBranches()
	if err != nil {
		return nil, err
	}
	if p.cur() != '}' {
		return nil, p.failf("expected '}' closing %s argument", typ)
	}
	p.pos++
	if typ == "select" {
		return &Select{Name: name, Branches: branches}, nil
	}
	return &Gender{Name: name, Branches: branches}, nil
}

// parseBranches parses "<selector> {msg}" pairs until the enclosing '}'.
func (p *parser) parseBranches() ([]Branch, error) {
	var branches []Branch
	for {
		p.skipSpaces()
		if p.cur() == '}' || p.pos >= len(p.raw) {
			break
		}
		sel, err := p.readSelector()
		if err != nil {
			return nil, err
		}
		p.skipSpaces()
		if p.cur() != '{' {
			return nil, p.failf("expected '{' after selector %q", sel)
		}
		p.pos++ // consume '{'
		value, err := p.parseMessage(true)
		if err != nil {
			return nil, err
		}
		if p.cur() != '}' {
			return nil, p.failf("expected '}' closing branch %q", sel)
		}
		p.pos++ // consume '}'
		branches = append(branches, Branch{Key: sel, Exact: strings.HasPrefix(sel, "="), Value: value})
	}
	return branches, nil
}

// readSelector reads a branch selector token up to the next '{'.
func (p *parser) readSelector() (string, error) {
	start := p.pos
	for p.pos < len(p.raw) && p.cur() != '{' && p.cur() != '}' {
		p.pos++
	}
	sel := strings.TrimSpace(p.raw[start:p.pos])
	if sel == "" {
		return "", p.failf("expected branch selector")
	}
	return sel, nil
}

func (p *parser) readNumber() (int, error) {
	start := p.pos
	if p.cur() == '-' {
		p.pos++
	}
	for p.pos < len(p.raw) && p.cur() >= '0' && p.cur() <= '9' {
		p.pos++
	}
	if start == p.pos || (p.raw[start] == '-' && p.pos == start+1) {
		return 0, p.failf("expected number")
	}
	var n int
	if _, err := fmt.Sscanf(p.raw[start:p.pos], "%d", &n); err != nil {
		return 0, p.failf("invalid number %q", p.raw[start:p.pos])
	}
	return n, nil
}

// matchWord consumes the given word if it appears at the current position.
func (p *parser) matchWord(word string) bool {
	if strings.HasPrefix(p.raw[p.pos:], word) {
		after := p.pos + len(word)
		if after >= len(p.raw) || !isNameChar(p.raw[after]) {
			p.pos = after
			return true
		}
	}
	return false
}

func isNameChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}
