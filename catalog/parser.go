package catalog

import (
	"fmt"
	"strconv"
	"strings"
)

// Parse 解析 ICU MessageFormat 子集：
//
//	text | '{' name '}' | '{' name ',' type '}'
//	     | '{' name ',' 'plural' ',' [offset:N] (=N{msg})*(kw{msg})+ '}'
//	     | '{' name ',' ('select'|'selectordinal') ',' (kw{msg})+ '}'
//
// 转义：” → '，'{ → {，'} → }，'# → #。
func Parse(lang, key, raw string) (*Message, error) {
	p := &parser{s: raw}
	nodes, err := p.parseMessage("")
	if err != nil {
		return nil, fmt.Errorf("%s:%s: %w", lang, key, err)
	}
	if p.pos != len(p.s) {
		return nil, fmt.Errorf("%s:%s: unexpected trailing content at %d", lang, key, p.pos)
	}
	return &Message{Key: key, Lang: lang, Raw: raw, Nodes: nodes}, nil
}

type parser struct {
	s   string
	pos int
}

// parseMessage 解析到 end 字符（"" 表示到结尾），返回节点列表。
func (p *parser) parseMessage(end string) ([]Node, error) {
	var nodes []Node
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			nodes = append(nodes, Text{Value: text.String()})
			text.Reset()
		}
	}
	for p.pos < len(p.s) {
		c := p.s[p.pos]
		if end != "" && c == end[0] {
			break
		}
		switch c {
		case '\'':
			p.pos++
			if p.pos < len(p.s) {
				switch p.s[p.pos] {
				case '\'':
					text.WriteByte('\'')
					p.pos++
				case '{', '}', '#':
					text.WriteByte(p.s[p.pos])
					p.pos++
				default:
					text.WriteByte('\'')
				}
			} else {
				text.WriteByte('\'')
			}
		case '{':
			flush()
			n, err := p.parsePlaceholder()
			if err != nil {
				return nil, err
			}
			nodes = append(nodes, n)
		case '}':
			if end == "" {
				return nil, fmt.Errorf("unmatched '}' at %d", p.pos)
			}
			text.WriteByte(c)
			p.pos++
		default:
			text.WriteByte(c)
			p.pos++
		}
	}
	flush()
	return nodes, nil
}

func (p *parser) parsePlaceholder() (Node, error) {
	p.pos++ // consume '{'
	p.skipWS()
	name := p.readIdent()
	if name == "" {
		return nil, fmt.Errorf("expected placeholder name at %d", p.pos)
	}
	p.skipWS()
	if p.pos < len(p.s) && p.s[p.pos] == '}' {
		p.pos++
		return Arg{Name: name}, nil
	}
	if err := p.expect(','); err != nil {
		return nil, err
	}
	p.skipWS()
	typ := p.readIdent()
	p.skipWS()
	switch typ {
	case "number", "date", "time":
		if err := p.expect('}'); err != nil {
			return nil, err
		}
		return Arg{Name: name, Type: typ}, nil
	case "plural", "selectordinal":
		return p.parsePlural(name)
	case "select":
		return p.parseSelect(name)
	default:
		return nil, fmt.Errorf("unknown placeholder type %q at %d", typ, p.pos)
	}
}

func (p *parser) parsePlural(name string) (Node, error) {
	if err := p.expect(','); err != nil {
		return nil, err
	}
	pl := Plural{Arg: name, Exacts: map[int][]Node{}, Variants: map[string][]Node{}}
	p.skipWS()
	// 可选 offset:N
	if strings.HasPrefix(p.s[p.pos:], "offset") {
		p.pos += len("offset")
		p.skipWS()
		if err := p.expect(':'); err != nil {
			return nil, err
		}
		p.skipWS()
		n := p.readNumber()
		pl.Offset = n
		p.skipWS()
	}
	for {
		p.skipWS()
		if p.pos >= len(p.s) {
			return nil, fmt.Errorf("unterminated plural for %q", name)
		}
		if p.s[p.pos] == '}' {
			p.pos++
			break
		}
		variant, body, err := p.parseVariant()
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(variant, "=") {
			n, err := strconv.Atoi(variant[1:])
			if err != nil {
				return nil, fmt.Errorf("bad exact match %q", variant)
			}
			pl.Exacts[n] = body
		} else {
			pl.Variants[variant] = body
		}
	}
	return pl, nil
}

func (p *parser) parseSelect(name string) (Node, error) {
	if err := p.expect(','); err != nil {
		return nil, err
	}
	sel := Select{Arg: name, Variants: map[string][]Node{}}
	for {
		p.skipWS()
		if p.pos >= len(p.s) {
			return nil, fmt.Errorf("unterminated select for %q", name)
		}
		if p.s[p.pos] == '}' {
			p.pos++
			break
		}
		variant, body, err := p.parseVariant()
		if err != nil {
			return nil, err
		}
		sel.Variants[variant] = body
	}
	return sel, nil
}

// parseVariant 解析 "kw{msg}"，msg 可嵌套。
func (p *parser) parseVariant() (string, []Node, error) {
	p.skipWS()
	kw := p.readWord()
	if kw == "" {
		return "", nil, fmt.Errorf("expected variant keyword at %d", p.pos)
	}
	p.skipWS()
	if err := p.expect('{'); err != nil {
		return "", nil, err
	}
	body, err := p.parseMessage("}")
	if err != nil {
		return "", nil, err
	}
	if err := p.expect('}'); err != nil {
		return "", nil, err
	}
	return kw, body, nil
}

func (p *parser) skipWS() {
	for p.pos < len(p.s) && (p.s[p.pos] == ' ' || p.s[p.pos] == '\t' || p.s[p.pos] == '\n') {
		p.pos++
	}
}

func (p *parser) expect(c byte) error {
	if p.pos >= len(p.s) || p.s[p.pos] != c {
		return fmt.Errorf("expected %q at %d", string(c), p.pos)
	}
	p.pos++
	return nil
}

func (p *parser) readIdent() string {
	start := p.pos
	for p.pos < len(p.s) {
		c := p.s[p.pos]
		if c == '_' || c == '.' || c == '-' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			p.pos++
		} else {
			break
		}
	}
	return p.s[start:p.pos]
}

// readWord 读取变体关键字，含 =N 形式。
func (p *parser) readWord() string {
	if p.pos < len(p.s) && p.s[p.pos] == '=' {
		start := p.pos
		p.pos++
		for p.pos < len(p.s) && p.s[p.pos] >= '0' && p.s[p.pos] <= '9' {
			p.pos++
		}
		return p.s[start:p.pos]
	}
	return p.readIdent()
}

func (p *parser) readNumber() int {
	start := p.pos
	for p.pos < len(p.s) && p.s[p.pos] >= '0' && p.s[p.pos] <= '9' {
		p.pos++
	}
	n, _ := strconv.Atoi(p.s[start:p.pos])
	return n
}
