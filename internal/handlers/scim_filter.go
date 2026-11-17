package handlers

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var errInvalidSCIMFilter = errors.New("invalid filter")

// SCIM filter operators (RFC 7644 §3.4.2.2).
const (
	FilterOpEq = "eq" // equals
	FilterOpNe = "ne" // not equals
	FilterOpCo = "co" // contains
	FilterOpSw = "sw" // starts with
	FilterOpEw = "ew" // ends with
	FilterOpGt = "gt" // greater than
	FilterOpGe = "ge" // greater than or equal
	FilterOpLt = "lt" // less than
	FilterOpLe = "le" // less than or equal
	FilterOpPr = "pr" // present (has value)
)

var scimCompareOps = map[string]bool{
	FilterOpEq: true,
	FilterOpNe: true,
	FilterOpCo: true,
	FilterOpSw: true,
	FilterOpEw: true,
	FilterOpGt: true,
	FilterOpGe: true,
	FilterOpLt: true,
	FilterOpLe: true,
}

// likeEscaper escapes SQL LIKE special characters to prevent pattern injection.
// This ensures %, _, and \ are treated as literal characters, not wildcards.
var likeEscaper = strings.NewReplacer(
	`\`, `\\`,
	`%`, `\%`,
	`_`, `\_`,
)

func escapeLikePattern(s string) string {
	return likeEscaper.Replace(s)
}

// Supported filter attributes for Users (lower-cased SCIM attr -> SQL column).
// Attribute names are case-insensitive (RFC 7643 §2.1), so keys are stored
// folded and lookups fold the incoming attribute the same way.
var userFilterAttrs = map[string]string{
	"username":        "username",
	"email":           "email",
	"emails.value":    "email",
	"displayname":     "first_name || ' ' || last_name",
	"name.givenname":  "first_name",
	"name.familyname": "last_name",
	"externalid":      "scim_external_id",
	"active":          "is_active",
}

// Supported filter attributes for Groups (lower-cased SCIM attr -> SQL column)
var groupFilterAttrs = map[string]string{
	"displayname": "name",
	"externalid":  "scim_external_id",
}

// SCIMFilterResult holds parsed filter data
type SCIMFilterResult struct {
	WhereClause string
	Args        []any
}

// =============================================================================
// Lexer
// =============================================================================

type scimTokenKind int

const (
	scimTokEOF scimTokenKind = iota
	scimTokWord
	scimTokString
	scimTokLParen
	scimTokRParen
	scimTokLBracket
	scimTokRBracket
)

type scimToken struct {
	kind scimTokenKind
	text string
}

type scimLexer struct {
	input string
	pos   int
}

// next returns the next token, skipping whitespace. Attribute paths, operators,
// keywords, and bare values all come back as words; the parser decides their
// role from position.
func (l *scimLexer) next() (scimToken, error) {
	for l.pos < len(l.input) {
		switch l.input[l.pos] {
		case ' ', '\t', '\n', '\r':
			l.pos++
			continue
		}
		break
	}
	if l.pos >= len(l.input) {
		return scimToken{kind: scimTokEOF}, nil
	}

	switch c := l.input[l.pos]; c {
	case '(':
		l.pos++
		return scimToken{kind: scimTokLParen, text: "("}, nil
	case ')':
		l.pos++
		return scimToken{kind: scimTokRParen, text: ")"}, nil
	case '[':
		l.pos++
		return scimToken{kind: scimTokLBracket, text: "["}, nil
	case ']':
		l.pos++
		return scimToken{kind: scimTokRBracket, text: "]"}, nil
	case '"':
		return l.readString()
	}

	start := l.pos
	for l.pos < len(l.input) {
		switch l.input[l.pos] {
		case ' ', '\t', '\n', '\r', '(', ')', '[', ']', '"':
			goto done
		}
		l.pos++
	}
done:
	return scimToken{kind: scimTokWord, text: l.input[start:l.pos]}, nil
}

func (l *scimLexer) readString() (scimToken, error) {
	l.pos++ // opening quote
	var b strings.Builder
	for l.pos < len(l.input) {
		c := l.input[l.pos]
		if c == '\\' && l.pos+1 < len(l.input) {
			if next := l.input[l.pos+1]; next == '"' || next == '\\' {
				b.WriteByte(next)
				l.pos += 2
				continue
			}
		}
		if c == '"' {
			l.pos++
			return scimToken{kind: scimTokString, text: b.String()}, nil
		}
		b.WriteByte(c)
		l.pos++
	}
	return scimToken{}, fmt.Errorf("unterminated string literal")
}

func tokenizeSCIMFilter(input string) ([]scimToken, error) {
	lex := &scimLexer{input: input}
	var tokens []scimToken
	for {
		tok, err := lex.next()
		if err != nil {
			return nil, err
		}
		if tok.kind == scimTokEOF {
			return tokens, nil
		}
		tokens = append(tokens, tok)
	}
}

// =============================================================================
// AST
// =============================================================================

type scimFilterNode interface{ scimFilterNode() }

type scimLogicalNode struct {
	op          string // "and" / "or"
	left, right scimFilterNode
}

func (*scimLogicalNode) scimFilterNode() {}

type scimNotNode struct{ operand scimFilterNode }

func (*scimNotNode) scimFilterNode() {}

type scimComparisonNode struct {
	path  string
	op    string
	value scimCompValue
}

func (*scimComparisonNode) scimFilterNode() {}

// scimValuePathNode is a multi-valued attribute selector such as
// emails[type eq "work"], optionally followed by a sub-attribute comparison
// (emails[type eq "work"].value eq "x").
type scimValuePathNode struct {
	base       string
	selector   scimFilterNode
	comparison *scimComparisonNode
}

func (*scimValuePathNode) scimFilterNode() {}

type scimCompKind int

const (
	scimCompString scimCompKind = iota
	scimCompNumber
	scimCompBool
	scimCompNull
)

type scimCompValue struct {
	kind   scimCompKind
	str    string
	number float64
	bool   bool
}

func (v scimCompValue) scalar() any {
	switch v.kind {
	case scimCompNumber:
		return v.number
	case scimCompBool:
		return v.bool
	default:
		return v.str
	}
}

func (v scimCompValue) scalarString() string {
	if v.kind == scimCompString {
		return v.str
	}
	return fmt.Sprintf("%v", v.scalar())
}

// =============================================================================
// Parser
// =============================================================================

type scimFilterParser struct {
	tokens []scimToken
	pos    int
}

func (p *scimFilterParser) peek() scimToken {
	if p.pos >= len(p.tokens) {
		return scimToken{kind: scimTokEOF}
	}
	return p.tokens[p.pos]
}

func (p *scimFilterParser) advance() {
	p.pos++
}

func (p *scimFilterParser) isKeyword(kw string) bool {
	tok := p.peek()
	return tok.kind == scimTokWord && strings.EqualFold(tok.text, kw)
}

// parseFilter parses a full filter expression: not > and > or.
func (p *scimFilterParser) parseFilter() (scimFilterNode, error) {
	return p.parseOr()
}

func (p *scimFilterParser) parseOr() (scimFilterNode, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.isKeyword("or") {
		p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &scimLogicalNode{op: "or", left: left, right: right}
	}
	return left, nil
}

func (p *scimFilterParser) parseAnd() (scimFilterNode, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.isKeyword("and") {
		p.advance()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = &scimLogicalNode{op: "and", left: left, right: right}
	}
	return left, nil
}

func (p *scimFilterParser) parseNot() (scimFilterNode, error) {
	if !p.isKeyword("not") {
		return p.parsePrimary()
	}
	p.advance()
	if p.peek().kind != scimTokLParen {
		return nil, fmt.Errorf("expected '(' after 'not'")
	}
	p.advance()
	operand, err := p.parseFilter()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != scimTokRParen {
		return nil, fmt.Errorf("expected ')' to close 'not'")
	}
	p.advance()
	return &scimNotNode{operand: operand}, nil
}

func (p *scimFilterParser) parsePrimary() (scimFilterNode, error) {
	if p.peek().kind == scimTokLParen {
		p.advance()
		node, err := p.parseFilter()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != scimTokRParen {
			return nil, fmt.Errorf("expected ')'")
		}
		p.advance()
		return node, nil
	}
	return p.parseAttrExp()
}

func (p *scimFilterParser) parseAttrExp() (scimFilterNode, error) {
	tok := p.peek()
	if tok.kind != scimTokWord {
		return nil, fmt.Errorf("expected attribute name")
	}
	p.advance()
	path := strings.ToLower(tok.text)

	if op, ok := p.peekCompareOp(); ok {
		p.advance()
		if op == FilterOpPr {
			return &scimComparisonNode{path: path, op: op}, nil
		}
		value, err := p.parseCompValue()
		if err != nil {
			return nil, err
		}
		return &scimComparisonNode{path: path, op: op, value: value}, nil
	}

	if p.peek().kind == scimTokLBracket {
		return p.parseValuePath(path)
	}

	return nil, fmt.Errorf("expected operator after attribute %q", path)
}

func (p *scimFilterParser) parseValuePath(base string) (scimFilterNode, error) {
	p.advance() // '['
	selector, err := p.parseFilter()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != scimTokRBracket {
		return nil, fmt.Errorf("expected ']'")
	}
	p.advance()

	node := &scimValuePathNode{base: base, selector: selector}

	// Optional trailing sub-attribute: emails[type eq "work"].value eq "x".
	sub := p.peek()
	if sub.kind != scimTokWord || !strings.HasPrefix(sub.text, ".") {
		return node, nil
	}
	subAttr := strings.ToLower(strings.TrimPrefix(sub.text, "."))
	p.advance()
	fullPath := base + "." + subAttr

	if op, ok := p.peekCompareOp(); ok {
		p.advance()
		value, err := p.parseCompValue()
		if err != nil {
			return nil, err
		}
		node.comparison = &scimComparisonNode{path: fullPath, op: op, value: value}
		return node, nil
	}
	return nil, fmt.Errorf("expected comparison after %q", fullPath)
}

// peekCompareOp reports whether the next token is a comparison or "pr"
// operator, without consuming it.
func (p *scimFilterParser) peekCompareOp() (string, bool) {
	tok := p.peek()
	if tok.kind != scimTokWord {
		return "", false
	}
	op := strings.ToLower(tok.text)
	if op == FilterOpPr || scimCompareOps[op] {
		return op, true
	}
	return "", false
}

func (p *scimFilterParser) parseCompValue() (scimCompValue, error) {
	tok := p.peek()
	switch tok.kind {
	case scimTokString:
		p.advance()
		return scimCompValue{kind: scimCompString, str: tok.text}, nil
	case scimTokWord:
		p.advance()
		switch strings.ToLower(tok.text) {
		case "true":
			return scimCompValue{kind: scimCompBool, bool: true}, nil
		case "false":
			return scimCompValue{kind: scimCompBool, bool: false}, nil
		case "null":
			return scimCompValue{kind: scimCompNull}, nil
		}
		if n, err := strconv.ParseFloat(tok.text, 64); err == nil {
			return scimCompValue{kind: scimCompNumber, number: n}, nil
		}
		// Lenient: an unquoted bareword is treated as a string value. The old
		// regex parser accepted this, and some clients emit it.
		return scimCompValue{kind: scimCompString, str: tok.text}, nil
	default:
		return scimCompValue{}, fmt.Errorf("expected comparison value")
	}
}

// =============================================================================
// SQL compiler
// =============================================================================

type scimFilterCompiler struct {
	attrMap map[string]string
	args    []any
}

func (c *scimFilterCompiler) compile(node scimFilterNode) (string, error) {
	switch n := node.(type) {
	case *scimLogicalNode:
		left, err := c.compile(n.left)
		if err != nil {
			return "", err
		}
		right, err := c.compile(n.right)
		if err != nil {
			return "", err
		}
		return "(" + left + " " + strings.ToUpper(n.op) + " " + right + ")", nil

	case *scimNotNode:
		operand, err := c.compile(n.operand)
		if err != nil {
			return "", err
		}
		return "NOT (" + operand + ")", nil

	case *scimComparisonNode:
		return c.compileComparison(n.path, n.op, n.value)

	case *scimValuePathNode:
		return c.compileValuePath(n)

	default:
		return "", fmt.Errorf("invalid filter expression")
	}
}

func (c *scimFilterCompiler) compileComparison(path, op string, value scimCompValue) (string, error) {
	col, ok := c.attrMap[path]
	if !ok {
		return "", fmt.Errorf("unsupported filter attribute: %s", path)
	}

	if op == FilterOpPr {
		return "(" + col + " IS NOT NULL AND " + col + " != '')", nil
	}

	// active is boolean; reject non-boolean comparisons so a bad filter is a
	// 400 rather than a query that silently matches the wrong rows.
	if path == "active" {
		b, err := activeBoolValue(value)
		if err != nil {
			return "", err
		}
		switch op {
		case FilterOpEq:
			c.args = append(c.args, b)
			return col + " = ?", nil
		case FilterOpNe:
			c.args = append(c.args, b)
			return col + " != ?", nil
		default:
			return "", fmt.Errorf("invalid filter: operator %q not supported for active", op)
		}
	}

	switch op {
	case FilterOpEq:
		if value.kind == scimCompNull {
			return col + " IS NULL", nil
		}
		c.args = append(c.args, value.scalar())
		return "LOWER(" + col + ") = LOWER(?)", nil

	case FilterOpNe:
		if value.kind == scimCompNull {
			return col + " IS NOT NULL", nil
		}
		c.args = append(c.args, value.scalar())
		return "LOWER(" + col + ") != LOWER(?)", nil

	case FilterOpCo:
		c.args = append(c.args, "%"+escapeLikePattern(value.scalarString())+"%")
		return "LOWER(" + col + ") LIKE LOWER(?) ESCAPE '\\'", nil

	case FilterOpSw:
		c.args = append(c.args, escapeLikePattern(value.scalarString())+"%")
		return "LOWER(" + col + ") LIKE LOWER(?) ESCAPE '\\'", nil

	case FilterOpEw:
		c.args = append(c.args, "%"+escapeLikePattern(value.scalarString()))
		return "LOWER(" + col + ") LIKE LOWER(?) ESCAPE '\\'", nil

	case FilterOpGt, FilterOpGe, FilterOpLt, FilterOpLe:
		sqlOp := map[string]string{FilterOpGt: ">", FilterOpGe: ">=", FilterOpLt: "<", FilterOpLe: "<="}[op]
		c.args = append(c.args, value.scalar())
		return "LOWER(" + col + ") " + sqlOp + " LOWER(?)", nil

	default:
		return "", fmt.Errorf("unsupported filter operator: %s", op)
	}
}

// compileValuePath resolves a multi-valued selector. A trailing comparison is
// compiled directly. Otherwise the selector is compiled against the base
// attribute's sub-attributes: sub-attributes Windshift does not persist (for
// example emails.type) become tautologies so a selector like type eq "work"
// does not reject otherwise-matching rows.
func (c *scimFilterCompiler) compileValuePath(n *scimValuePathNode) (string, error) {
	if n.comparison != nil {
		return c.compileComparison(n.comparison.path, n.comparison.op, n.comparison.value)
	}

	baseCol, ok := c.attrMap[n.base]
	if !ok {
		// A bare multi-valued selector such as emails[...] resolves to its
		// value sub-attribute, which is the only persisted component.
		baseCol, ok = c.attrMap[n.base+".value"]
	}
	if !ok {
		return "", fmt.Errorf("unsupported filter attribute: %s", n.base)
	}

	clause, err := c.compileSubFilter(n.selector, n.base)
	if err != nil {
		return "", err
	}
	if clause == "" || clause == "1=1" {
		return "(" + baseCol + " IS NOT NULL AND " + baseCol + " != '')", nil
	}
	return clause, nil
}

func (c *scimFilterCompiler) compileSubFilter(node scimFilterNode, base string) (string, error) {
	switch n := node.(type) {
	case *scimLogicalNode:
		left, err := c.compileSubFilter(n.left, base)
		if err != nil {
			return "", err
		}
		right, err := c.compileSubFilter(n.right, base)
		if err != nil {
			return "", err
		}
		return "(" + left + " " + strings.ToUpper(n.op) + " " + right + ")", nil

	case *scimNotNode:
		operand, err := c.compileSubFilter(n.operand, base)
		if err != nil {
			return "", err
		}
		return "NOT (" + operand + ")", nil

	case *scimComparisonNode:
		full := base + "." + n.path
		if _, ok := c.attrMap[full]; !ok {
			return "1=1", nil
		}
		return c.compileComparison(full, n.op, n.value)

	default:
		return "", fmt.Errorf("nested value paths are not supported")
	}
}

func activeBoolValue(value scimCompValue) (bool, error) {
	switch value.kind {
	case scimCompBool:
		return value.bool, nil
	case scimCompString:
		return parseSCIMBool(value.str)
	default:
		return false, fmt.Errorf("invalid filter: boolean value must be 'true' or 'false'")
	}
}

// =============================================================================
// Public entry points
// =============================================================================

// ParseSCIMFilter parses a SCIM filter string into a SQL WHERE clause and args.
// It supports the RFC 7644 §3.4.2.2 grammar: not > and > or precedence,
// parenthesized grouping, the full comparison operator set, "pr", and
// multi-valued value paths such as emails[type eq "work"].value.
func ParseSCIMFilter(filter, resourceType string) (*SCIMFilterResult, error) {
	if strings.TrimSpace(filter) == "" {
		return &SCIMFilterResult{WhereClause: "", Args: nil}, nil
	}

	var attrMap map[string]string
	switch resourceType {
	case "User":
		attrMap = userFilterAttrs
	case "Group":
		attrMap = groupFilterAttrs
	default:
		return nil, fmt.Errorf("unsupported resource type: %s", resourceType)
	}

	tokens, err := tokenizeSCIMFilter(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter syntax: %w", err)
	}

	parser := &scimFilterParser{tokens: tokens}
	node, err := parser.parseFilter()
	if err != nil {
		return nil, fmt.Errorf("invalid filter syntax: %w", err)
	}
	if parser.peek().kind != scimTokEOF {
		return nil, fmt.Errorf("invalid filter syntax: unexpected token %q", parser.peek().text)
	}

	compiler := &scimFilterCompiler{attrMap: attrMap}
	clause, err := compiler.compile(node)
	if err != nil {
		return nil, err
	}
	return &SCIMFilterResult{WhereClause: clause, Args: compiler.args}, nil
}

// ParseSCIMFilterWithAnd is retained for callers that historically split on
// top-level "and"; the full parser now handles and/or/not directly.
func ParseSCIMFilterWithAnd(filter, resourceType string) (*SCIMFilterResult, error) {
	return ParseSCIMFilter(filter, resourceType)
}

// parseSCIMBool accepts only true or false so invalid filters return SCIM 400
// rather than successful results with incorrect rows.
func parseSCIMBool(value string) (bool, error) {
	switch strings.ToLower(value) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("invalid filter: boolean value must be 'true' or 'false', got %q", value)
	}
}

// splitTopLevelAnd recognizes case-insensitive top-level `and` outside quoted
// strings and parentheses. Its narrow byte lexer honors quoted escapes without
// implementing a full SCIM grammar.
func splitTopLevelAnd(filter string) []string {
	if filter == "" {
		return nil
	}
	var parts []string
	depth := 0
	inQuote := false
	start := 0
	i := 0
	for i < len(filter) {
		c := filter[i]
		if inQuote {
			switch c {
			case '\\':
				if i+1 < len(filter) {
					i += 2
					continue
				}
			case '"':
				inQuote = false
			}
			i++
			continue
		}
		switch c {
		case '"':
			inQuote = true
			i++
			continue
		case '(':
			depth++
			i++
			continue
		case ')':
			if depth > 0 {
				depth--
			}
			i++
			continue
		}
		if depth == 0 && c == ' ' && hasFoldedAndAt(filter, i) {
			parts = append(parts, filter[start:i])
			i += len(" and ")
			start = i
			continue
		}
		i++
	}
	parts = append(parts, filter[start:])
	return parts
}

// hasFoldedAndAt reports whether s[i:] begins with the 5-byte sequence
// " and " under ASCII case-folding (the `a` and `d` characters are
// matched case-insensitively).
func hasFoldedAndAt(s string, i int) bool {
	if i+5 > len(s) {
		return false
	}
	if s[i] != ' ' || s[i+4] != ' ' {
		return false
	}
	a, n, d := s[i+1], s[i+2], s[i+3]
	return (a == 'a' || a == 'A') && (n == 'n' || n == 'N') && (d == 'd' || d == 'D')
}

// stripParens removes wrapping parentheses from a filter term.
// e.g. "(userName eq \"john\")" -> "userName eq \"john\""
func stripParens(s string) string {
	s = strings.TrimSpace(s)
	for strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		// Verify the parens are balanced (the opening paren matches the closing one)
		depth := 0
		matched := true
		for i, ch := range s {
			switch ch {
			case '(':
				depth++
			case ')':
				depth--
			}
			if depth == 0 && i < len(s)-1 {
				matched = false
				break
			}
		}
		if !matched {
			break
		}
		s = s[1 : len(s)-1]
		s = strings.TrimSpace(s)
	}
	return s
}

// ExtractResourceTypeFilter extracts a meta.resourceType filter from a SCIM filter string.
// It returns the resource type value and the remaining filter with the resourceType term removed.
// If no meta.resourceType filter is found, it returns empty string and the original filter.
func ExtractResourceTypeFilter(filter string) (resourceType, remainingFilter string) {
	if filter == "" {
		return "", ""
	}

	parts := splitTopLevelAnd(filter)

	var remaining []string
	for _, part := range parts {
		stripped := stripParens(strings.TrimSpace(part))
		// Check if this is a meta.resourceType filter
		rtPattern := regexp.MustCompile(`(?i)^meta\.resourceType\s+eq\s+(?:"([^"]*)"|(\S+))$`)
		if matches := rtPattern.FindStringSubmatch(stripped); matches != nil {
			resourceType = matches[1]
			if resourceType == "" {
				resourceType = matches[2]
			}
			continue
		}
		remaining = append(remaining, strings.TrimSpace(part))
	}

	return resourceType, strings.Join(remaining, " and ")
}
