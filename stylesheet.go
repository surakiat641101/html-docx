package htmldocx

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// This file implements the subset of CSS needed to apply <style> blocks:
// type, universal, .class, #id and [attr] selectors, the :first-child,
// :last-child, :nth-child() and :root pseudo-classes, the descendant, child
// (>), adjacent (+) and sibling (~) combinators, specificity, source order
// and !important. Rules inside @media are used only for print/all/screen
// queries; other at-rules are ignored.

type cssRule struct {
	sel               *complexSelector
	spec, order       int
	normal, important map[string]string
}

type stylesheet struct {
	rules []cssRule
}

var cssComment = regexp.MustCompile(`(?s)/\*.*?\*/`)

func (ss *stylesheet) add(src string) {
	ss.parse(cssComment.ReplaceAllString(src, " "))
}

func (ss *stylesheet) parse(src string) {
	for i := 0; i < len(src); {
		open := strings.IndexByte(src[i:], '{')
		if open < 0 {
			return
		}
		open += i
		end := matchingBrace(src, open)
		prelude := src[i:open]
		// Drop statements that precede the rule, e.g. @import ...; or a stray }.
		if k := strings.LastIndexAny(prelude, ";}"); k >= 0 {
			prelude = prelude[k+1:]
		}
		prelude = strings.TrimSpace(prelude)
		body := src[open+1 : end]
		i = end + 1

		if strings.HasPrefix(prelude, "@") {
			if q, ok := strings.CutPrefix(strings.ToLower(prelude), "@media"); ok && mediaApplies(q) {
				ss.parse(body)
			}
			continue
		}
		normal, important := parseDecls(body)
		if normal == nil && important == nil {
			continue
		}
		for _, s := range splitTopLevel(prelude, ',') {
			sel, spec, ok := parseSelector(s)
			if !ok {
				continue
			}
			ss.rules = append(ss.rules, cssRule{sel: sel, spec: spec, order: len(ss.rules), normal: normal, important: important})
		}
	}
}

func matchingBrace(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(s)
}

func mediaApplies(q string) bool {
	q = strings.TrimSpace(q)
	return q == "" || strings.Contains(q, "print") || q == "all" || q == "screen"
}

// splitTopLevel splits s on sep outside of parentheses and brackets.
func splitTopLevel(s string, sep byte) []string {
	var parts []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case sep:
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, s[start:])
}

// computeStyle returns the cascaded declarations for n: matching stylesheet
// rules by specificity and order, then the inline style, with !important
// declarations on top.
func (c *converter) computeStyle(n *html.Node) map[string]string {
	inline, inlineImp := parseDecls(getAttr(n, "style"))
	if len(c.sheet.rules) == 0 && inlineImp == nil {
		return inline
	}
	var hits []*cssRule
	for i := range c.sheet.rules {
		if r := &c.sheet.rules[i]; r.sel.match(n) {
			hits = append(hits, r)
		}
	}
	if len(hits) == 0 && inlineImp == nil {
		return inline
	}
	sort.SliceStable(hits, func(a, b int) bool {
		if hits[a].spec != hits[b].spec {
			return hits[a].spec < hits[b].spec
		}
		return hits[a].order < hits[b].order
	})
	out := make(map[string]string)
	merge := func(m map[string]string) {
		for k, v := range m {
			out[k] = v
		}
	}
	for _, r := range hits {
		merge(r.normal)
	}
	merge(inline)
	for _, r := range hits {
		merge(r.important)
	}
	merge(inlineImp)
	return out
}

// collectStyles gathers every <style> element of the document in order.
func (c *converter) collectStyles(n *html.Node) {
	if n.Type == html.ElementNode && strings.EqualFold(n.Data, "style") {
		if media := strings.ToLower(getAttr(n, "media")); media == "" || mediaApplies(media) {
			c.sheet.add(textContent(n))
		}
		return
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		c.collectStyles(ch)
	}
}

// ---------------------------------------------------------------- selectors

type attrSelector struct {
	name, op, val string
}

type compoundSelector struct {
	tag     string // "" or "*" = any
	id      string
	classes []string
	attrs   []attrSelector
	nth     [][2]int // :nth-child(an+b) as {a, b}; :first-child = {0, 1}
	last    bool     // :last-child
	root    bool     // :root
}

type complexSelector struct {
	parts []compoundSelector
	combs []byte // combs[i] joins parts[i] and parts[i+1]: ' ', '>', '+', '~'
}

func parseSelector(s string) (*complexSelector, int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, 0, false
	}
	sel := &complexSelector{}
	var cur compoundSelector
	empty := true
	pending := byte(0)
	ids, classes, tags := 0, 0, 0

	push := func() bool {
		if empty {
			return pending == 0
		}
		if len(sel.parts) > 0 {
			if pending == 0 {
				pending = ' '
			}
			sel.combs = append(sel.combs, pending)
		}
		sel.parts = append(sel.parts, cur)
		cur, empty, pending = compoundSelector{}, true, 0
		return true
	}

	for i := 0; i < len(s); {
		ch := s[i]
		switch {
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r':
			if !empty {
				push()
			}
			i++
		case ch == '>' || ch == '+' || ch == '~':
			if !empty {
				push()
			}
			if len(sel.parts) == 0 {
				return nil, 0, false
			}
			pending = ch
			i++
		case ch == '*':
			cur.tag, empty = "*", false
			i++
		case ch == '#':
			id, n := readIdent(s[i+1:])
			if id == "" {
				return nil, 0, false
			}
			cur.id, empty = id, false
			ids++
			i += 1 + n
		case ch == '.':
			cl, n := readIdent(s[i+1:])
			if cl == "" {
				return nil, 0, false
			}
			cur.classes, empty = append(cur.classes, cl), false
			classes++
			i += 1 + n
		case ch == '[':
			end := strings.IndexByte(s[i:], ']')
			if end < 0 {
				return nil, 0, false
			}
			a, ok := parseAttrSelector(s[i+1 : i+end])
			if !ok {
				return nil, 0, false
			}
			cur.attrs, empty = append(cur.attrs, a), false
			classes++
			i += end + 1
		case ch == ':':
			if strings.HasPrefix(s[i:], "::") {
				return nil, 0, false // pseudo-elements never match real nodes
			}
			name, n := readIdent(s[i+1:])
			i += 1 + n
			arg := ""
			if i < len(s) && s[i] == '(' {
				end := strings.IndexByte(s[i:], ')')
				if end < 0 {
					return nil, 0, false
				}
				arg = strings.TrimSpace(s[i+1 : i+end])
				i += end + 1
			}
			switch strings.ToLower(name) {
			case "first-child":
				cur.nth = append(cur.nth, [2]int{0, 1})
			case "last-child":
				cur.last = true
			case "nth-child":
				ab, ok := parseNth(arg)
				if !ok {
					return nil, 0, false
				}
				cur.nth = append(cur.nth, ab)
			case "root":
				cur.root = true
			default:
				return nil, 0, false // :hover, :not(), ... cannot apply to a static document
			}
			empty = false
			classes++
		default:
			tag, n := readIdent(s[i:])
			if tag == "" {
				return nil, 0, false
			}
			cur.tag, empty = strings.ToLower(tag), false
			tags++
			i += n
		}
	}
	if !push() || len(sel.parts) == 0 {
		return nil, 0, false
	}
	return sel, ids*10000 + classes*100 + tags, true
}

func readIdent(s string) (string, int) {
	i := 0
	for i < len(s) {
		ch := s[i]
		if ch == '-' || ch == '_' || ch >= 0x80 || ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' {
			i++
			continue
		}
		if ch == '\\' && i+1 < len(s) {
			i += 2
			continue
		}
		break
	}
	return strings.ReplaceAll(s[:i], `\`, ""), i
}

func parseAttrSelector(s string) (attrSelector, bool) {
	for _, op := range []string{"~=", "^=", "$=", "*=", "|=", "="} {
		if k, v, ok := strings.Cut(s, op); ok {
			v = strings.TrimSpace(v)
			if f := strings.Fields(v); len(f) == 2 && strings.EqualFold(f[1], "i") {
				v = f[0] // case-insensitivity flag is ignored
			}
			v = strings.Trim(v, `"'`)
			return attrSelector{strings.ToLower(strings.TrimSpace(k)), op, v}, strings.TrimSpace(k) != ""
		}
	}
	name := strings.ToLower(strings.TrimSpace(s))
	return attrSelector{name: name}, name != ""
}

var nthPattern = regexp.MustCompile(`^([+-]?\d*)n(?:\s*([+-])\s*(\d+))?$`)

// parseNth parses the argument of :nth-child() into {a, b} for an+b.
func parseNth(s string) ([2]int, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "odd":
		return [2]int{2, 1}, true
	case "even":
		return [2]int{2, 0}, true
	}
	if n, err := strconv.Atoi(s); err == nil {
		return [2]int{0, n}, true
	}
	m := nthPattern.FindStringSubmatch(s)
	if m == nil {
		return [2]int{}, false
	}
	a := 1
	switch m[1] {
	case "", "+":
	case "-":
		a = -1
	default:
		a, _ = strconv.Atoi(m[1])
	}
	b := 0
	if m[3] != "" {
		b, _ = strconv.Atoi(m[3])
		if m[2] == "-" {
			b = -b
		}
	}
	return [2]int{a, b}, true
}

func (s *complexSelector) match(n *html.Node) bool {
	return s.matchAt(len(s.parts)-1, n)
}

func (s *complexSelector) matchAt(i int, n *html.Node) bool {
	if !s.parts[i].match(n) {
		return false
	}
	if i == 0 {
		return true
	}
	switch s.combs[i-1] {
	case '>':
		p := parentElement(n)
		return p != nil && s.matchAt(i-1, p)
	case '+':
		p := prevElement(n)
		return p != nil && s.matchAt(i-1, p)
	case '~':
		for p := prevElement(n); p != nil; p = prevElement(p) {
			if s.matchAt(i-1, p) {
				return true
			}
		}
	default:
		for p := parentElement(n); p != nil; p = parentElement(p) {
			if s.matchAt(i-1, p) {
				return true
			}
		}
	}
	return false
}

func (c *compoundSelector) match(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	if c.tag != "" && c.tag != "*" && !strings.EqualFold(n.Data, c.tag) {
		return false
	}
	if c.id != "" && getAttr(n, "id") != c.id {
		return false
	}
	if len(c.classes) > 0 {
		have := strings.Fields(getAttr(n, "class"))
		for _, want := range c.classes {
			if !contains(have, want) {
				return false
			}
		}
	}
	for _, a := range c.attrs {
		if !a.match(n) {
			return false
		}
	}
	if c.root && parentElement(n) != nil {
		return false
	}
	if c.last && nextElement(n) != nil {
		return false
	}
	if len(c.nth) > 0 {
		idx := 1
		for p := prevElement(n); p != nil; p = prevElement(p) {
			idx++
		}
		for _, ab := range c.nth {
			if !nthMatches(ab[0], ab[1], idx) {
				return false
			}
		}
	}
	return true
}

func nthMatches(a, b, idx int) bool {
	if a == 0 {
		return idx == b
	}
	d := idx - b
	return d%a == 0 && d/a >= 0
}

func (a attrSelector) match(n *html.Node) bool {
	if !hasAttr(n, a.name) {
		return false
	}
	v := getAttr(n, a.name)
	switch a.op {
	case "":
		return true
	case "=":
		return v == a.val
	case "~=":
		return contains(strings.Fields(v), a.val)
	case "^=":
		return a.val != "" && strings.HasPrefix(v, a.val)
	case "$=":
		return a.val != "" && strings.HasSuffix(v, a.val)
	case "*=":
		return a.val != "" && strings.Contains(v, a.val)
	case "|=":
		return v == a.val || strings.HasPrefix(v, a.val+"-")
	}
	return false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func parentElement(n *html.Node) *html.Node {
	if p := n.Parent; p != nil && p.Type == html.ElementNode {
		return p
	}
	return nil
}

func prevElement(n *html.Node) *html.Node {
	for p := n.PrevSibling; p != nil; p = p.PrevSibling {
		if p.Type == html.ElementNode {
			return p
		}
	}
	return nil
}

func nextElement(n *html.Node) *html.Node {
	for p := n.NextSibling; p != nil; p = p.NextSibling {
		if p.Type == html.ElementNode {
			return p
		}
	}
	return nil
}
