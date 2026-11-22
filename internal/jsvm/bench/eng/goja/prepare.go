package main

import (
	"github.com/paul-nameless/qi/internal/jsvm/bench/harness"
	"sort"
	"strings"

	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"
	"github.com/dop251/goja/token"
)

type edit struct {
	at  int
	del int
	ins string
}

const head = "(async function(){\n"

func patternNames(e ast.Expression, out *[]string) {
	switch t := e.(type) {
	case *ast.Identifier:
		*out = append(*out, string(t.Name))
	case *ast.ObjectPattern:
		for _, p := range t.Properties {
			switch pp := p.(type) {
			case *ast.PropertyShort:
				*out = append(*out, string(pp.Name.Name))
			case *ast.PropertyKeyed:
				patternNames(pp.Value, out)
			}
		}
		if t.Rest != nil {
			patternNames(t.Rest, out)
		}
	case *ast.ArrayPattern:
		for _, el := range t.Elements {
			if el != nil {
				patternNames(el, out)
			}
		}
		if t.Rest != nil {
			patternNames(t.Rest, out)
		}
	case *ast.AssignExpression:
		patternNames(t.Left, out)
	}
}

func (g *gvm) Prepare(code string) (string, bool, string) {
	plain, _ := harness.Wrap(code, "iife")
	src := head + code + "\n})"
	prog, err := parser.ParseFile(nil, "cell.js", src, 0)
	if err != nil || len(prog.Body) != 1 {
		return plain, false, ""
	}
	es, ok := prog.Body[0].(*ast.ExpressionStatement)
	if !ok {
		return plain, false, ""
	}
	fn, ok := es.Expression.(*ast.FunctionLiteral)
	if !ok {
		return plain, false, ""
	}
	var edits []edit
	var funcs []string
	list := fn.Body.List
	bodyStart := int(fn.Body.LeftBrace)
	bodyEnd := int(fn.Body.RightBrace) - 1
	startOf := func(i int) int {
		p := bodyStart
		if i > 0 {
			p = int(list[i-1].Idx1()) - 1
		}
		for p < bodyEnd && strings.ContainsRune(" \t\r\n;)", rune(src[p])) {
			p++
		}
		return p
	}
	endOf := func(i int) int {
		p := bodyEnd
		if i+1 < len(list) {
			p = startOf(i + 1)
		}
		for p > 0 && strings.ContainsRune(" \t\r\n;", rune(src[p-1])) {
			p--
		}
		return p
	}
	bindings := func(i, kw, kwLen int, bl []*ast.Binding) {
		simple := true
		for _, b := range bl {
			if _, ok := b.Target.(*ast.Identifier); !ok {
				simple = false
			}
		}
		if !simple {
			if len(bl) == 1 && bl[0].Initializer != nil {
				edits = append(edits, edit{kw - 1, kwLen, "("}, edit{endOf(i), 0, "\n)"})
			}
			return
		}
		edits = append(edits, edit{kw - 1, kwLen, ""})
		for _, b := range bl {
			if id := b.Target.(*ast.Identifier); b.Initializer == nil {
				edits = append(edits, edit{int(id.Idx1()) - 1, 0, " = globalThis." + string(id.Name)})
			}
		}
	}
	for i, st := range list {
		switch t := st.(type) {
		case *ast.VariableStatement:
			bindings(i, int(t.Var), 3, t.List)
		case *ast.LexicalDeclaration:
			n := 3
			if t.Token == token.CONST {
				n = 5
			}
			bindings(i, int(t.Idx), n, t.List)
		case *ast.FunctionDeclaration:
			if t.Function.Name != nil {
				funcs = append(funcs, string(t.Function.Name.Name))
			}
		case *ast.ClassDeclaration:
			if t.Class.Name != nil {
				edits = append(edits, edit{int(t.Class.Class) - 1, 0, string(t.Class.Name.Name) + " = "})
			}
		case *ast.ExpressionStatement:
			if i == len(list)-1 {
				edits = append(edits, edit{startOf(i), 0, "return ("}, edit{endOf(i), 0, "\n)"})
			}
		}
	}
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].at > edits[j].at })
	out := src
	for _, e := range edits {
		if e.at < 0 || e.at+e.del > len(out) {
			return plain, false, ""
		}
		out = out[:e.at] + e.ins + out[e.at+e.del:]
	}
	pre := ""
	for _, f := range funcs {
		pre += "globalThis." + f + " = " + f + "; "
	}
	body := strings.TrimPrefix(out, head)
	body = strings.TrimSuffix(body, "\n})")
	return "void (async function(){ " + pre + "\n" + body + "\n})().then(v => __host('done', '1', __fmt(v), ''), e => __host('done', '0', __err(e), ''))", false, ""
}
