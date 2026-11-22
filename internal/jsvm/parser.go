package jsvm

import "fmt"

type fnCtx struct {
	async    bool
	topLevel bool
	hasAwait bool
	inFunc   bool
}

type parser struct {
	lex      lexer
	tok      token
	prevEnd  int
	fn       *fnCtx
	notArrow map[int]bool
	parens   map[node]bool
	loops    int
	switches int
	depth    int
}

const maxNesting = 400

func (p *parser) enter() {
	p.depth++
	if p.depth > maxNesting {
		p.fail(p.tok.pos, "too deeply nested (limit %d levels)", maxNesting)
	}
}

func parseProgram(src string) (fn *funcNode, err error) {
	p := &parser{lex: lexer{src: src}, notArrow: map[int]bool{}, parens: map[node]bool{}}
	defer func() {
		if r := recover(); r != nil {
			se, ok := r.(*syntaxErr)
			if !ok {
				panic(r)
			}
			err = se
		}
	}()
	p.fn = &fnCtx{async: true, topLevel: true}
	p.next()
	body := []node{}
	for p.tok.t != tEOF {
		body = append(body, p.parseStatement())
	}
	return &funcNode{nodePos: nodePos{0}, body: body, async: p.fn.hasAwait, hasAwait: p.fn.hasAwait, end: len(src)}, nil
}

func (e *syntaxErr) Error() string { return "SyntaxError: " + e.msg }

func (p *parser) next() {
	p.prevEnd = p.tok.end
	p.tok = p.lex.next()
}

func (p *parser) fail(pos int, f string, a ...any) {
	p.lex.err = nil
	p.lex.fail(pos, fmt.Sprintf(f, a...))
}

func (p *parser) unexpected() {
	t := p.tok
	switch t.t {
	case tEOF:
		p.fail(t.pos, "Unexpected end of input")
	case tStr:
		p.fail(t.pos, "Unexpected string")
	case tNum:
		p.fail(t.pos, "Unexpected number")
	case tTemplate:
		p.fail(t.pos, "Unexpected template string")
	case tIdent:
		p.fail(t.pos, "Unexpected identifier '%s'", t.s)
	}
	p.fail(t.pos, "Unexpected token '%s'", p.lex.src[t.pos:t.end])
}

func (p *parser) unsupported(pos int, what string) {
	p.fail(pos, "unsupported: %s", what)
}

func (p *parser) is(s string) bool {
	return p.tok.t == tPunct && p.tok.s == s
}

func (p *parser) isKw(s string) bool {
	return p.tok.t == tKeyword && p.tok.s == s
}

func (p *parser) isIdent(s string) bool {
	return p.tok.t == tIdent && p.tok.s == s && !p.tok.escaped
}

func (p *parser) eat(s string) bool {
	if p.is(s) {
		p.next()
		return true
	}
	return false
}

func (p *parser) expect(s string) {
	if !p.is(s) {
		p.unexpected()
	}
	p.next()
}

func (p *parser) peek() token {
	save := p.lex.pos
	t := p.lex.next()
	p.lex.pos = save
	return t
}

func (p *parser) semicolon() {
	if p.eat(";") {
		return
	}
	if p.is("}") || p.tok.t == tEOF || p.tok.nl {
		return
	}
	p.unexpected()
}

func (p *parser) parseStatement() node {
	p.enter()
	n := p.parseStatementInner()
	p.depth--
	return n
}

func (p *parser) parseStatementInner() node {
	t := p.tok
	switch t.t {
	case tPunct:
		switch t.s {
		case "{":
			return p.parseBlock()
		case ";":
			p.next()
			return &emptyStmt{nodePos{t.pos}}
		}
	case tKeyword:
		switch t.s {
		case "var", "const", "let":
			d := p.parseVarDecl(false)
			p.semicolon()
			return d
		case "function":
			return p.parseFunction(true, false, t.pos)
		case "class":
			p.unsupported(t.pos, "classes")
		case "if":
			return p.parseIf()
		case "for":
			return p.parseFor()
		case "while":
			p.next()
			p.expect("(")
			test := p.parseExpression(false)
			p.expect(")")
			body := p.parseLoopBody()
			return &whileStmt{nodePos{t.pos}, test, body}
		case "do":
			p.next()
			body := p.parseLoopBody()
			if !p.isKw("while") {
				p.unexpected()
			}
			p.next()
			p.expect("(")
			test := p.parseExpression(false)
			p.expect(")")
			p.eat(";")
			return &doWhileStmt{nodePos{t.pos}, body, test}
		case "return":
			if !p.fn.inFunc && !p.fn.topLevel {
				p.fail(t.pos, "Illegal return statement")
			}
			p.next()
			var arg node
			if !p.is(";") && !p.is("}") && p.tok.t != tEOF && !p.tok.nl {
				arg = p.parseExpression(false)
			}
			p.semicolon()
			return &returnStmt{nodePos{t.pos}, arg}
		case "break", "continue":
			p.next()
			if p.tok.t == tIdent && !p.tok.nl {
				p.unsupported(p.tok.pos, "labels")
			}
			p.semicolon()
			if t.s == "break" {
				if p.loops == 0 && p.switches == 0 {
					p.fail(t.pos, "Illegal break statement")
				}
				return &breakStmt{nodePos{t.pos}}
			}
			if p.loops == 0 {
				p.fail(t.pos, "Illegal continue statement: no surrounding iteration statement")
			}
			return &continueStmt{nodePos{t.pos}}
		case "throw":
			p.next()
			if p.tok.nl {
				p.fail(t.pos, "Illegal newline after throw")
			}
			arg := p.parseExpression(false)
			p.semicolon()
			return &throwStmt{nodePos{t.pos}, arg}
		case "try":
			return p.parseTry()
		case "switch":
			return p.parseSwitch()
		case "debugger":
			p.next()
			p.semicolon()
			return &emptyStmt{nodePos{t.pos}}
		case "with":
			p.unsupported(t.pos, "with")
		case "import", "export":
			p.unsupported(t.pos, "modules ("+t.s+")")
		}
	case tIdent:
		if t.s == "async" && !t.escaped {
			nt := p.peek()
			if nt.t == tKeyword && nt.s == "function" && !nt.nl {
				p.next()
				return p.parseFunction(true, true, t.pos)
			}
		}
		if t.s == "using" && !t.escaped {
			nt := p.peek()
			if nt.t == tIdent && !nt.nl {
				p.unsupported(t.pos, "using declarations")
			}
		}
		nt := p.peek()
		if nt.t == tPunct && nt.s == ":" {
			p.unsupported(t.pos, "labels")
		}
	}
	e := p.parseExpression(false)
	p.semicolon()
	return &exprStmt{nodePos{t.pos}, e}
}

func (p *parser) parseLoopBody() node {
	p.loops++
	b := p.parseSubStatement()
	p.loops--
	return b
}

func (p *parser) parseBlock() *blockStmt {
	pos := p.tok.pos
	p.expect("{")
	var body []node
	for !p.is("}") {
		if p.tok.t == tEOF {
			p.unexpected()
		}
		body = append(body, p.parseStatement())
	}
	p.next()
	return &blockStmt{nodePos: nodePos{pos}, body: body}
}

func (p *parser) parseVarDecl(noIn bool) *varDecl {
	d := &varDecl{nodePos: nodePos{p.tok.pos}, kind: p.tok.s}
	p.next()
	for {
		pos := p.tok.pos
		target := p.parseBindingTarget()
		var init node
		if p.eat("=") {
			init = p.parseAssign(noIn)
		} else if !noIn {
			p.checkInit(d.kind, target, pos)
		}
		d.decls = append(d.decls, &declarator{nodePos{pos}, target, init})
		if !p.eat(",") {
			break
		}
	}
	return d
}

func (p *parser) checkInit(kind string, target node, pos int) {
	if _, ok := target.(*identNode); !ok {
		p.fail(pos, "Missing initializer in destructuring declaration")
	}
	if kind == "const" {
		p.fail(pos, "Missing initializer in const declaration")
	}
}

func (p *parser) parseBindingTarget() node {
	p.enter()
	n := p.parseBindingTargetInner()
	p.depth--
	return n
}

func (p *parser) parseBindingTargetInner() node {
	t := p.tok
	switch {
	case p.is("["):
		return p.parseArrayPattern()
	case p.is("{"):
		return p.parseObjectPattern()
	case t.t == tIdent:
		p.checkBindingName(t)
		p.next()
		return &identNode{nodePos: nodePos{t.pos}, name: t.s}
	}
	p.unexpected()
	return nil
}

func (p *parser) checkBindingName(t token) {
	switch t.s {
	case "arguments":
		p.unsupported(t.pos, "arguments")
	case "eval":
		p.unsupported(t.pos, "eval")
	case "await":
		if p.fn.async {
			p.fail(t.pos, "Unexpected reserved word")
		}
	}
}

func (p *parser) parseBindingElement() node {
	pos := p.tok.pos
	target := p.parseBindingTarget()
	if p.eat("=") {
		return &defaultNode{nodePos{pos}, target, p.parseAssign(false)}
	}
	return target
}

func (p *parser) parseArrayPattern() node {
	pos := p.tok.pos
	p.expect("[")
	var elems []node
	for !p.is("]") {
		if p.is(",") {
			p.next()
			elems = append(elems, nil)
			continue
		}
		if p.is("...") {
			rp := p.tok.pos
			p.next()
			elems = append(elems, &restNode{nodePos{rp}, p.parseBindingTarget()})
			if !p.is("]") {
				p.fail(p.tok.pos, "Rest element must be last element")
			}
			break
		}
		elems = append(elems, p.parseBindingElement())
		if !p.is("]") {
			p.expect(",")
		}
	}
	p.next()
	return &arrayPattern{nodePos{pos}, elems}
}

func (p *parser) parseObjectPattern() node {
	pos := p.tok.pos
	p.expect("{")
	op := &objectPattern{nodePos: nodePos{pos}}
	for !p.is("}") {
		if p.is("...") {
			p.next()
			op.rest = p.parseBindingTarget()
			if !p.is("}") {
				p.fail(p.tok.pos, "Rest element must be last element")
			}
			break
		}
		kp := p.tok.pos
		key, computed, name := p.parsePropertyKey()
		prop := &propNode{nodePos: nodePos{kp}, key: key, computed: computed}
		if p.eat(":") {
			prop.value = p.parseBindingElement()
		} else {
			if name == "" || computed {
				p.unexpected()
			}
			p.checkBindingName(token{s: name, pos: kp})
			prop.shorthand = true
			var v node = &identNode{nodePos: nodePos{kp}, name: name}
			if p.eat("=") {
				v = &defaultNode{nodePos{kp}, v, p.parseAssign(false)}
			}
			prop.value = v
		}
		op.props = append(op.props, prop)
		if !p.is("}") {
			p.expect(",")
		}
	}
	p.next()
	return op
}

func (p *parser) parsePropertyKey() (node, bool, string) {
	t := p.tok
	switch t.t {
	case tIdent, tKeyword:
		p.next()
		return &strNode{nodePos{t.pos}, t.s}, false, identName(t)
	case tStr:
		p.next()
		return &strNode{nodePos{t.pos}, t.s}, false, ""
	case tNum:
		p.next()
		return &strNode{nodePos{t.pos}, numberToString(t.n)}, false, ""
	case tPrivate:
		p.unsupported(t.pos, "classes (private names)")
	case tPunct:
		if t.s == "[" {
			p.next()
			k := p.parseAssign(false)
			p.expect("]")
			return k, true, ""
		}
	}
	p.unexpected()
	return nil, false, ""
}

func identName(t token) string {
	if t.t == tIdent {
		return t.s
	}
	switch t.s {
	case "true", "false", "null", "this", "typeof", "void", "delete", "new", "in", "instanceof", "function", "class", "super", "import", "export", "enum":
		return ""
	}
	if t.t == tKeyword {
		return ""
	}
	return t.s
}

func (p *parser) parseIf() node {
	pos := p.tok.pos
	p.next()
	p.expect("(")
	test := p.parseExpression(false)
	p.expect(")")
	cons := p.parseSubStatement()
	var alt node
	if p.isKw("else") {
		p.next()
		alt = p.parseSubStatement()
	}
	return &ifStmt{nodePos{pos}, test, cons, alt}
}

func (p *parser) parseSubStatement() node {
	if p.isKw("function") || p.isKw("class") {
		p.fail(p.tok.pos, "In strict mode code, functions can only be declared at top level or inside a block.")
	}
	if p.isKw("let") || p.isKw("const") {
		p.fail(p.tok.pos, "Lexical declaration cannot appear in a single-statement context")
	}
	return p.parseStatement()
}

func (p *parser) parseFor() node {
	pos := p.tok.pos
	p.next()
	if p.isIdent("await") {
		p.unsupported(p.tok.pos, "for await")
	}
	p.expect("(")
	var init node
	if p.is(";") {
	} else if p.isKw("var") || p.isKw("let") || p.isKw("const") {
		d := p.parseVarDecl(true)
		if p.isIdent("of") || p.isKw("in") {
			of := p.isIdent("of")
			if len(d.decls) != 1 || d.decls[0].init != nil {
				p.fail(d.pos, "Invalid left-hand side in for-%s loop: Must have a single binding.", map[bool]string{true: "of", false: "in"}[of])
			}
			return p.parseForInOf(pos, d, of)
		}
		for _, dc := range d.decls {
			if dc.init == nil {
				p.checkInit(d.kind, dc.target, dc.pos)
			}
		}
		init = d
	} else {
		e := p.parseExpression(true)
		if p.isIdent("of") || p.isKw("in") {
			return p.parseForInOf(pos, p.toPattern(e), p.isIdent("of"))
		}
		init = &exprStmt{nodePos{e.at()}, e}
	}
	p.expect(";")
	var test, update node
	if !p.is(";") {
		test = p.parseExpression(false)
	}
	p.expect(";")
	if !p.is(")") {
		update = p.parseExpression(false)
	}
	p.expect(")")
	body := p.parseLoopBody()
	return &forStmt{nodePos: nodePos{pos}, init: init, test: test, update: update, body: body}
}

func (p *parser) parseForInOf(pos int, left node, of bool) node {
	p.next()
	var right node
	if of {
		right = p.parseAssign(false)
	} else {
		right = p.parseExpression(false)
	}
	p.expect(")")
	body := p.parseLoopBody()
	return &forInStmt{nodePos: nodePos{pos}, of: of, left: left, right: right, body: body}
}

func (p *parser) parseTry() node {
	pos := p.tok.pos
	p.next()
	t := &tryStmt{nodePos: nodePos{pos}, block: p.parseBlock()}
	if p.isKw("catch") {
		p.next()
		if p.eat("(") {
			t.param = p.parseBindingTarget()
			p.expect(")")
		}
		t.handler = p.parseBlock()
	}
	if p.isKw("finally") {
		p.next()
		t.finalizer = p.parseBlock()
	}
	if t.handler == nil && t.finalizer == nil {
		p.fail(p.tok.pos, "Missing catch or finally after try")
	}
	return t
}

func (p *parser) parseSwitch() node {
	pos := p.tok.pos
	p.next()
	p.expect("(")
	disc := p.parseExpression(false)
	p.expect(")")
	p.expect("{")
	s := &switchStmt{nodePos: nodePos{pos}, disc: disc}
	p.switches++
	hasDefault := false
	for !p.is("}") {
		c := &switchCase{}
		if p.isKw("case") {
			p.next()
			c.test = p.parseExpression(false)
		} else if p.isKw("default") {
			if hasDefault {
				p.fail(p.tok.pos, "More than one default clause in switch statement")
			}
			hasDefault = true
			p.next()
		} else {
			p.unexpected()
		}
		p.expect(":")
		for !p.is("}") && !p.isKw("case") && !p.isKw("default") {
			if p.tok.t == tEOF {
				p.unexpected()
			}
			c.body = append(c.body, p.parseStatement())
		}
		s.cases = append(s.cases, c)
	}
	p.switches--
	p.next()
	return s
}

func (p *parser) parseFunction(decl, async bool, pos int) node {
	p.next()
	if p.is("*") {
		p.unsupported(p.tok.pos, "generators")
	}
	name := ""
	if p.tok.t == tIdent {
		p.checkBindingName(p.tok)
		name = p.tok.s
		p.next()
	} else if decl {
		p.unexpected()
	}
	f := &funcNode{nodePos: nodePos{pos}, name: name, async: async, isDecl: decl}
	p.parseFuncRest(f)
	return f
}

func (p *parser) parseFuncRest(f *funcNode) {
	saved, sl, ss := p.fn, p.loops, p.switches
	p.fn = &fnCtx{async: f.async, inFunc: true}
	p.loops, p.switches = 0, 0
	f.params = p.parseParams()
	f.body = p.parseFuncBody()
	f.end = p.prevEnd
	p.fn, p.loops, p.switches = saved, sl, ss
}

func (p *parser) parseParams() []node {
	p.expect("(")
	var params []node
	for !p.is(")") {
		if p.is("...") {
			rp := p.tok.pos
			p.next()
			params = append(params, &restNode{nodePos{rp}, p.parseBindingTarget()})
			if !p.is(")") {
				p.fail(p.tok.pos, "Rest parameter must be last formal parameter")
			}
			break
		}
		params = append(params, p.parseBindingElement())
		if !p.is(")") {
			p.expect(",")
		}
	}
	p.next()
	return params
}

func (p *parser) parseFuncBody() []node {
	p.expect("{")
	body := []node{}
	for !p.is("}") {
		if p.tok.t == tEOF {
			p.unexpected()
		}
		body = append(body, p.parseStatement())
	}
	p.next()
	return body
}

func (p *parser) parseExpression(noIn bool) node {
	e := p.parseAssign(noIn)
	if p.is(",") {
		s := &seqNode{nodePos{e.at()}, []node{e}}
		for p.eat(",") {
			s.list = append(s.list, p.parseAssign(noIn))
		}
		return s
	}
	return e
}

func isAssignOp(s string) bool {
	switch s {
	case "=", "+=", "-=", "*=", "/=", "%=", "**=", "<<=", ">>=", ">>>=", "&=", "|=", "^=", "&&=", "||=", "??=":
		return true
	}
	return false
}

func (p *parser) parseAssign(noIn bool) node {
	p.enter()
	n := p.parseAssignInner(noIn)
	p.depth--
	return n
}

func (p *parser) parseAssignInner(noIn bool) node {
	if a := p.tryArrow(); a != nil {
		return a
	}
	if p.isKw("yield") {
		p.unsupported(p.tok.pos, "generators")
	}
	start := p.tok.pos
	left := p.parseConditional(noIn)
	if p.tok.t == tPunct && isAssignOp(p.tok.s) {
		op := p.tok.s
		opPos := p.tok.pos
		if op == "=" {
			left = p.toPattern(left)
		} else if !isSimpleTarget(left) {
			p.fail(start, "Invalid left-hand side in assignment")
		}
		p.next()
		value := p.parseAssign(noIn)
		return &assignNode{nodePos{opPos}, op, left, value}
	}
	return left
}

func isSimpleTarget(n node) bool {
	switch t := n.(type) {
	case *identNode:
		return true
	case *memberNode:
		return !t.optional
	}
	return false
}

func (p *parser) toPattern(n node) node {
	switch t := n.(type) {
	case *identNode:
		p.checkBindingName(token{s: t.name, pos: t.pos})
		return t
	case *memberNode:
		if t.optional {
			p.fail(t.pos, "Invalid left-hand side in assignment")
		}
		return t
	case *arrayNode:
		ap := &arrayPattern{nodePos: t.nodePos}
		for i, e := range t.elems {
			if e == nil {
				ap.elems = append(ap.elems, nil)
				continue
			}
			if s, ok := e.(*spreadNode); ok {
				if i != len(t.elems)-1 {
					p.fail(s.pos, "Rest element must be last element")
				}
				ap.elems = append(ap.elems, &restNode{s.nodePos, p.toPattern(s.arg)})
				continue
			}
			ap.elems = append(ap.elems, p.toPattern(e))
		}
		return ap
	case *objectNode:
		op := &objectPattern{nodePos: t.nodePos}
		for i, pr := range t.props {
			if pr.spread {
				if i != len(t.props)-1 {
					p.fail(pr.pos, "Rest element must be last element")
				}
				op.rest = p.toPattern(pr.value)
				continue
			}
			if pr.method {
				p.fail(pr.pos, "Invalid destructuring assignment target")
			}
			np := &propNode{nodePos: pr.nodePos, key: pr.key, computed: pr.computed, shorthand: pr.shorthand}
			np.value = p.toPattern(pr.value)
			op.props = append(op.props, np)
		}
		return op
	case *assignNode:
		if t.op == "=" {
			return &defaultNode{t.nodePos, p.toPattern(t.target), t.value}
		}
	case *defaultNode:
		return &defaultNode{t.nodePos, p.toPattern(t.target), t.value}
	case *arrayPattern, *objectPattern:
		return t
	}
	p.fail(n.at(), "Invalid destructuring assignment target")
	return nil
}

func (p *parser) tryArrow() node {
	t := p.tok
	async := false
	if t.t == tIdent && t.s == "async" && !t.escaped {
		nt := p.peek()
		if !nt.nl && (nt.t == tIdent || (nt.t == tPunct && nt.s == "(")) {
			async = true
		}
	}
	if t.t == tIdent && !async {
		nt := p.peek()
		if nt.t == tPunct && nt.s == "=>" && !nt.nl {
			p.checkBindingName(t)
			p.next()
			p.next()
			f := &funcNode{nodePos: nodePos{t.pos}, arrow: true, params: []node{&identNode{nodePos: nodePos{t.pos}, name: t.s}}}
			p.parseArrowBody(f)
			return f
		}
		return nil
	}
	if !async && !p.is("(") {
		return nil
	}
	if p.notArrow[t.pos] {
		return nil
	}
	saveTok, saveLex, savePrev, saveDepth := p.tok, p.lex.pos, p.prevEnd, p.depth
	params, ok := p.speculateArrow(async)
	if ok {
		p.next()
		f := &funcNode{nodePos: nodePos{t.pos}, arrow: true, async: async, params: params}
		p.parseArrowBody(f)
		return f
	}
	p.notArrow[t.pos] = true
	p.tok, p.lex.pos, p.prevEnd, p.depth = saveTok, saveLex, savePrev, saveDepth
	p.lex.err = nil
	return nil
}

func (p *parser) speculateArrow(async bool) (params []node, ok bool) {
	savedFn := p.fn
	defer func() {
		if r := recover(); r != nil {
			if _, is := r.(*syntaxErr); !is {
				panic(r)
			}
			p.fn = savedFn
			params, ok = nil, false
		}
	}()
	if async {
		p.next()
	}
	if async && p.tok.t == tIdent {
		params = []node{&identNode{nodePos: nodePos{p.tok.pos}, name: p.tok.s}}
		p.checkBindingName(p.tok)
		p.next()
	} else {
		p.fn = &fnCtx{async: async, inFunc: true}
		params = p.parseParams()
		p.fn = savedFn
	}
	if !p.is("=>") || p.tok.nl {
		return nil, false
	}
	return params, true
}

func (p *parser) parseArrowBody(f *funcNode) {
	saved, sl, ss := p.fn, p.loops, p.switches
	p.fn = &fnCtx{async: f.async, inFunc: true}
	p.loops, p.switches = 0, 0
	if p.is("{") {
		f.body = p.parseFuncBody()
	} else {
		f.exprBody = p.parseAssign(false)
	}
	f.end = p.prevEnd
	p.fn, p.loops, p.switches = saved, sl, ss
}

func (p *parser) parseConditional(noIn bool) node {
	test := p.parseBinary(0, noIn)
	if !p.is("?") {
		return test
	}
	pos := p.tok.pos
	p.next()
	cons := p.parseAssign(false)
	p.expect(":")
	alt := p.parseAssign(noIn)
	return &condNode{nodePos{pos}, test, cons, alt}
}

func binPrec(t token, noIn bool) int {
	if t.t == tKeyword {
		switch t.s {
		case "instanceof":
			return 8
		case "in":
			if noIn {
				return 0
			}
			return 8
		}
		return 0
	}
	if t.t != tPunct {
		return 0
	}
	switch t.s {
	case "??":
		return 1
	case "||":
		return 2
	case "&&":
		return 3
	case "|":
		return 4
	case "^":
		return 5
	case "&":
		return 6
	case "==", "!=", "===", "!==":
		return 7
	case "<", ">", "<=", ">=":
		return 8
	case "<<", ">>", ">>>":
		return 9
	case "+", "-":
		return 10
	case "*", "/", "%":
		return 11
	case "**":
		return 12
	}
	return 0
}

func (p *parser) parseBinary(minPrec int, noIn bool) node {
	saved := p.depth
	n := p.parseBinaryChain(minPrec, noIn)
	p.depth = saved
	return n
}

func (p *parser) parseBinaryChain(minPrec int, noIn bool) node {
	bare := p.startsUnary()
	left := p.parseUnary()
	for {
		p.enter()
		prec := binPrec(p.tok, noIn)
		if prec == 0 || prec <= minPrec && !(prec == 12 && minPrec == 12) {
			return left
		}
		op := p.tok.s
		pos := p.tok.pos
		if op == "**" && bare {
			switch left.(type) {
			case *unaryNode, *awaitNode:
				p.fail(left.at(), "Unary operator used immediately before exponentiation expression. Parenthesis must be used to disambiguate operator precedence")
			}
		}
		bare = false
		p.next()
		var right node
		if op == "**" {
			right = p.parseBinary(11, noIn)
		} else {
			right = p.parseBinary(prec, noIn)
		}
		switch op {
		case "&&", "||", "??":
			p.checkNullishMix(op, pos, left, right)
			left = &logicalNode{nodePos{pos}, op, left, right}
		default:
			left = &binaryNode{nodePos{pos}, op, left, right}
		}
	}
}

func (p *parser) startsUnary() bool {
	t := p.tok
	switch t.t {
	case tPunct:
		switch t.s {
		case "!", "~", "+", "-":
			return true
		}
	case tKeyword:
		switch t.s {
		case "typeof", "void", "delete":
			return true
		}
	case tIdent:
		return t.s == "await" && p.fn.async
	}
	return false
}

func (p *parser) checkNullishMix(op string, pos int, operands ...node) {
	for _, n := range operands {
		l, ok := n.(*logicalNode)
		if !ok || p.parens[l] {
			continue
		}
		if (op == "??") != (l.op == "??") {
			p.fail(pos, "Unexpected token '%s'", op)
		}
	}
}

func (p *parser) parseUnary() node {
	p.enter()
	n := p.parseUnaryInner()
	p.depth--
	return n
}

func (p *parser) parseUnaryInner() node {
	t := p.tok
	if t.t == tPunct {
		switch t.s {
		case "!", "~", "+", "-":
			p.next()
			return &unaryNode{nodePos{t.pos}, t.s, p.parseUnary()}
		case "++", "--":
			p.next()
			arg := p.parseUnary()
			if !isSimpleTarget(arg) {
				p.fail(arg.at(), "Invalid left-hand side expression in prefix operation")
			}
			return &updateNode{nodePos{t.pos}, t.s, true, arg}
		}
	}
	if t.t == tKeyword {
		switch t.s {
		case "typeof", "void", "delete":
			p.next()
			arg := p.parseUnary()
			if t.s == "delete" {
				if _, ok := arg.(*identNode); ok {
					p.fail(t.pos, "Delete of an unqualified identifier in strict mode.")
				}
			}
			return &unaryNode{nodePos{t.pos}, t.s, arg}
		}
	}
	if t.t == tIdent && t.s == "await" && !t.escaped && p.fn.async {
		p.next()
		p.fn.hasAwait = true
		return &awaitNode{nodePos{t.pos}, p.parseUnary()}
	}
	e := p.parsePostfix()
	return e
}

func (p *parser) parsePostfix() node {
	e := p.parseLHS()
	if p.tok.t == tPunct && (p.tok.s == "++" || p.tok.s == "--") && !p.tok.nl {
		if !isSimpleTarget(e) {
			p.fail(e.at(), "Invalid left-hand side expression in postfix operation")
		}
		op := p.tok.s
		p.next()
		return &updateNode{nodePos{e.at()}, op, false, e}
	}
	return e
}

func (p *parser) parseLHS() node {
	var e node
	if p.isKw("new") {
		e = p.parseNew()
	} else {
		e = p.parsePrimary()
	}
	return p.parseCallTail(e, false)
}

func (p *parser) parseNew() node {
	p.enter()
	n := p.parseNewInner()
	p.depth--
	return n
}

func (p *parser) parseNewInner() node {
	pos := p.tok.pos
	p.next()
	if p.is(".") {
		p.unsupported(pos, "new.target")
	}
	var callee node
	if p.isKw("new") {
		callee = p.parseNew()
	} else {
		callee = p.parsePrimary()
	}
	callee = p.parseCallTail(callee, true)
	var args []node
	if p.is("(") {
		args = p.parseArgs()
	}
	return &newNode{nodePos{pos}, callee, args}
}

func (p *parser) parseArgs() []node {
	p.expect("(")
	args := []node{}
	for !p.is(")") {
		if p.is("...") {
			sp := p.tok.pos
			p.next()
			args = append(args, &spreadNode{nodePos{sp}, p.parseAssign(false)})
		} else {
			args = append(args, p.parseAssign(false))
		}
		if !p.is(")") {
			p.expect(",")
		}
	}
	p.next()
	return args
}

func callPos(callee node) int {
	if m, ok := callee.(*memberNode); ok {
		return m.pos
	}
	return callee.at()
}

func (p *parser) parseCallTail(e node, noCall bool) node {
	saved := p.depth
	n := p.parseCallChain(e, noCall)
	p.depth = saved
	return n
}

func (p *parser) parseCallChain(e node, noCall bool) node {
	inChain := false
	for {
		p.enter()
		t := p.tok
		switch {
		case p.is("."):
			p.next()
			if p.tok.t == tPrivate {
				p.unsupported(p.tok.pos, "classes (private names)")
			}
			if p.tok.t != tIdent && p.tok.t != tKeyword {
				p.unexpected()
			}
			e = &memberNode{nodePos: nodePos{p.tok.pos}, obj: e, name: p.tok.s}
			p.next()
		case p.is("?."):
			if noCall {
				p.fail(t.pos, "Invalid optional chain from new expression")
			}
			inChain = true
			p.next()
			switch {
			case p.is("("):
				e = &callNode{nodePos{callPos(e)}, e, p.parseArgs(), true}
			case p.is("["):
				p.next()
				prop := p.parseExpression(false)
				p.expect("]")
				e = &memberNode{nodePos: nodePos{t.pos}, obj: e, prop: prop, optional: true}
			case p.tok.t == tIdent || p.tok.t == tKeyword:
				e = &memberNode{nodePos: nodePos{p.tok.pos}, obj: e, name: p.tok.s, optional: true}
				p.next()
			case p.tok.t == tPrivate:
				p.unsupported(p.tok.pos, "classes (private names)")
			default:
				p.unexpected()
			}
		case p.is("["):
			p.next()
			prop := p.parseExpression(false)
			p.expect("]")
			e = &memberNode{nodePos: nodePos{t.pos}, obj: e, prop: prop}
		case p.is("(") && !noCall:
			e = &callNode{nodePos{callPos(e)}, e, p.parseArgs(), false}
		case t.t == tTemplate:
			if inChain {
				p.fail(t.pos, "Invalid tagged template on optional chain")
			}
			if !isStringRaw(e) {
				p.unsupported(t.pos, "tagged templates other than String.raw")
			}
			tm := p.parseTemplate()
			tm.tag = e
			e = tm
		default:
			if inChain {
				return &chainNode{nodePos{e.at()}, e}
			}
			return e
		}
	}
}

func isStringRaw(e node) bool {
	m, ok := e.(*memberNode)
	if !ok || m.name != "raw" || m.prop != nil {
		return false
	}
	id, ok := m.obj.(*identNode)
	return ok && id.name == "String"
}

func (p *parser) parseTemplate() *tmplNode {
	p.enter()
	n := p.parseTemplateInner()
	p.depth--
	return n
}

func (p *parser) parseTemplateInner() *tmplNode {
	t := p.tok
	tm := &tmplNode{nodePos: nodePos{t.pos}}
	for {
		tm.quasis = append(tm.quasis, t.s)
		tm.raws = append(tm.raws, t.raw)
		if t.bad != "" {
			tm.quasis[len(tm.quasis)-1] = "\x00bad:" + t.bad
		}
		if t.tmplEnd {
			p.next()
			return tm
		}
		p.next()
		tm.exprs = append(tm.exprs, p.parseExpression(false))
		if !p.is("}") {
			p.unexpected()
		}
		p.lex.pos = p.tok.pos + 1
		t = p.lex.scanTemplate(p.tok.pos)
		p.tok = t
	}
}

func (p *parser) parsePrimary() node {
	t := p.tok
	switch t.t {
	case tIdent:
		if t.s == "async" && !t.escaped {
			nt := p.peek()
			if nt.t == tKeyword && nt.s == "function" && !nt.nl {
				p.next()
				return p.parseFunction(false, true, t.pos)
			}
		}
		if t.s == "arguments" {
			p.unsupported(t.pos, "arguments")
		}
		if t.s == "eval" {
			p.unsupported(t.pos, "eval")
		}
		if t.s == "await" && p.fn.async {
			p.unexpected()
		}
		p.next()
		return &identNode{nodePos: nodePos{t.pos}, name: t.s}
	case tNum:
		p.next()
		return &numNode{nodePos{t.pos}, t.n}
	case tStr:
		p.next()
		return &strNode{nodePos{t.pos}, t.s}
	case tTemplate:
		tm := p.parseTemplate()
		for _, q := range tm.quasis {
			if len(q) > 5 && q[:5] == "\x00bad:" {
				p.fail(t.pos, "%s", q[5:])
			}
		}
		return tm
	case tPrivate:
		p.unsupported(t.pos, "classes (private names)")
	case tKeyword:
		switch t.s {
		case "true", "false":
			p.next()
			return &litNode{nodePos{t.pos}, boolV(t.s == "true")}
		case "null":
			p.next()
			return &litNode{nodePos{t.pos}, null}
		case "this":
			p.next()
			return &thisNode{nodePos{t.pos}}
		case "function":
			return p.parseFunction(false, false, t.pos)
		case "class":
			p.unsupported(t.pos, "classes")
		case "super":
			p.unsupported(t.pos, "classes (super)")
		case "import":
			p.unsupported(t.pos, "modules (import)")
		case "new":
			return p.parseNew()
		}
	case tPunct:
		switch t.s {
		case "(":
			p.next()
			e := p.parseExpression(false)
			p.expect(")")
			if l, ok := e.(*logicalNode); ok {
				p.parens[l] = true
			}
			return e
		case "[":
			return p.parseArrayLiteral()
		case "{":
			return p.parseObjectLiteral()
		case "/", "/=":
			rt := p.lex.scanRegex(t.pos)
			p.tok = rt
			p.next()
			return &regexNode{nodePos{t.pos}, rt.s, rt.raw}
		}
	}
	p.unexpected()
	return nil
}

func (p *parser) parseArrayLiteral() node {
	pos := p.tok.pos
	p.next()
	a := &arrayNode{nodePos: nodePos{pos}}
	for !p.is("]") {
		if p.is(",") {
			p.next()
			a.elems = append(a.elems, nil)
			continue
		}
		if p.is("...") {
			sp := p.tok.pos
			p.next()
			a.elems = append(a.elems, &spreadNode{nodePos{sp}, p.parseAssign(false)})
		} else {
			a.elems = append(a.elems, p.parseAssign(false))
		}
		if !p.is("]") {
			p.expect(",")
		}
	}
	p.next()
	return a
}

func (p *parser) parseObjectLiteral() node {
	pos := p.tok.pos
	p.next()
	o := &objectNode{nodePos: nodePos{pos}}
	for !p.is("}") {
		o.props = append(o.props, p.parseObjectProp())
		if !p.is("}") {
			p.expect(",")
		}
	}
	p.next()
	return o
}

func (p *parser) parseObjectProp() *propNode {
	t := p.tok
	if p.is("...") {
		p.next()
		return &propNode{nodePos: nodePos{t.pos}, spread: true, value: p.parseAssign(false)}
	}
	if p.is("*") {
		p.unsupported(t.pos, "generators")
	}
	async := false
	if t.t == tIdent && (t.s == "get" || t.s == "set" || t.s == "async") && !t.escaped {
		nt := p.peek()
		isKey := nt.t == tIdent || nt.t == tKeyword || nt.t == tStr || nt.t == tNum || (nt.t == tPunct && (nt.s == "[" || nt.s == "*"))
		if isKey && !nt.nl {
			if t.s != "async" {
				p.unsupported(t.pos, "getters/setters")
			}
			async = true
			p.next()
			if p.is("*") {
				p.unsupported(p.tok.pos, "async generators")
			}
		}
	}
	kp := p.tok.pos
	key, computed, name := p.parsePropertyKey()
	prop := &propNode{nodePos: nodePos{kp}, key: key, computed: computed}
	switch {
	case p.is("("):
		f := &funcNode{nodePos: nodePos{kp}, async: async, method: true}
		if !computed {
			f.name = key.(*strNode).v
		}
		p.parseFuncRest(f)
		prop.value = f
		prop.method = true
	case async:
		p.unexpected()
	case p.eat(":"):
		prop.value = p.parseAssign(false)
	case name != "" && !computed:
		if name == "arguments" {
			p.unsupported(kp, "arguments")
		}
		prop.shorthand = true
		var v node = &identNode{nodePos: nodePos{kp}, name: name}
		if p.is("=") {
			p.next()
			v = &defaultNode{nodePos{kp}, v, p.parseAssign(false)}
		}
		prop.value = v
	default:
		p.unexpected()
	}
	return prop
}
