package jsvm

type bindKind uint8

const (
	bVar bindKind = iota
	bLet
	bConst
	bParam
	bFunc
	bCatch
	bSelf
)

type binding struct {
	name     string
	kind     bindKind
	fn       *funcScope
	captured bool
	idx      int
	global   bool
	inited   bool
	always   bool
	tdz      bool
}

type ref struct {
	b     *binding
	name  string
	up    int
	check bool
}

type scope struct {
	vars    map[string]*binding
	order   []*binding
	parent  *scope
	fn      *funcScope
	funcs   []*funcNode
	isFunc  bool
	capture bool
	params  *scope
}

type funcScope struct {
	node    *funcNode
	parent  *funcScope
	ups     []*binding
	upIndex map[*binding]int
	all     []*binding
	nslots  int
	ncells  int
	top     bool
}

type resolver struct {
	fn        *funcScope
	sc        *scope
	declaring bool
	height    int
}

const maxHeight = 1000

func (r *resolver) down(n node) {
	r.height++
	if r.height > maxHeight {
		r.fail(n.at(), "too deeply nested (limit 1000 levels)")
	}
}

func resolveProgram(prog *funcNode) (err error) {
	defer func() {
		if r := recover(); r != nil {
			se, ok := r.(*syntaxErr)
			if !ok {
				panic(r)
			}
			err = se
		}
	}()
	r := &resolver{}
	r.function(prog, true)
	return nil
}

func (r *resolver) fail(pos int, msg string) {
	panic(&syntaxErr{msg: msg, pos: pos})
}

func (r *resolver) pushScope(isFunc bool) *scope {
	s := &scope{vars: map[string]*binding{}, parent: r.sc, fn: r.fn, isFunc: isFunc}
	r.sc = s
	return s
}

func (r *resolver) popScope() { r.sc = r.sc.parent }

func (r *resolver) declare(s *scope, name string, kind bindKind, pos int) *binding {
	if s.params != nil {
		if pb, ok := s.params.vars[name]; ok {
			if kind == bLet || kind == bConst {
				r.fail(pos, "Identifier '"+name+"' has already been declared")
			}
			if kind == bVar {
				return pb
			}
		}
	}
	if b, ok := s.vars[name]; ok {
		lexical := kind == bLet || kind == bConst || b.kind == bLet || b.kind == bConst
		if lexical {
			r.fail(pos, "Identifier '"+name+"' has already been declared")
		}
		if kind == bFunc {
			b.kind = bFunc
		}
		return b
	}
	b := &binding{name: name, kind: kind, fn: s.fn, inited: kind != bLet && kind != bConst && kind != bParam}
	if s.fn.top && s.isFunc {
		b.global = true
	}
	s.vars[name] = b
	s.order = append(s.order, b)
	s.fn.all = append(s.fn.all, b)
	return b
}

func (r *resolver) function(f *funcNode, top bool) {
	fs := &funcScope{node: f, parent: r.fn, upIndex: map[*binding]int{}, top: top}
	savedFn, savedSc := r.fn, r.sc
	if f.name != "" && !f.isDecl && !f.arrow && !f.method {
		r.fn = fs
		self := r.pushScope(false)
		self.fn = fs
		f.selfRef = r.declare(self, f.name, bSelf, f.pos)
	}
	r.fn = fs
	s := r.pushScope(true)
	f.scope = s
	f.simple = true
	for _, p := range f.params {
		if _, ok := p.(*identNode); !ok {
			f.simple = false
		}
		for _, id := range boundNames(p, nil) {
			r.declare(s, id.name, bParam, id.pos)
		}
	}
	if f.simple {
		for _, b := range s.order {
			b.inited = true
		}
	}
	if f.exprBody != nil {
		for _, p := range f.params {
			r.declPattern(p)
		}
		r.expr(f.exprBody)
	} else if f.simple {
		r.hoistVars(s, f.body)
		r.hoistLexical(s, f.body)
		for _, p := range f.params {
			r.declPattern(p)
		}
		r.stmts(f.body)
	} else {
		for _, p := range f.params {
			r.declPattern(p)
		}
		bs := r.pushScope(false)
		bs.params = s
		f.bodyScope = bs
		r.hoistVars(bs, f.body)
		r.hoistLexical(bs, f.body)
		r.stmts(f.body)
	}
	r.fn, r.sc = savedFn, savedSc
	r.assignSlots(fs)
}

func (r *resolver) assignSlots(fs *funcScope) {
	for _, b := range fs.all {
		if b.global {
			continue
		}
		if b.captured {
			b.idx = fs.ncells
			fs.ncells++
		} else {
			b.idx = fs.nslots
			fs.nslots++
		}
	}
}

func boundNames(n node, out []*identNode) []*identNode {
	switch t := n.(type) {
	case *identNode:
		out = append(out, t)
	case *defaultNode:
		out = boundNames(t.target, out)
	case *restNode:
		out = boundNames(t.arg, out)
	case *arrayPattern:
		for _, e := range t.elems {
			if e != nil {
				out = boundNames(e, out)
			}
		}
	case *objectPattern:
		for _, p := range t.props {
			out = boundNames(p.value, out)
		}
		if t.rest != nil {
			out = boundNames(t.rest, out)
		}
	}
	return out
}

func (r *resolver) hoistVars(s *scope, body []node) {
	for _, st := range body {
		r.hoistVar(s, st)
	}
}

func (r *resolver) hoistVar(s *scope, st node) {
	switch t := st.(type) {
	case *varDecl:
		if t.kind == "var" {
			for _, d := range t.decls {
				for _, id := range boundNames(d.target, nil) {
					r.declare(s, id.name, bVar, id.pos)
				}
			}
		}
	case *blockStmt:
		r.hoistVars(s, t.body)
	case *ifStmt:
		r.hoistVar(s, t.cons)
		if t.alt != nil {
			r.hoistVar(s, t.alt)
		}
	case *forStmt:
		if t.init != nil {
			r.hoistVar(s, t.init)
		}
		r.hoistVar(s, t.body)
	case *forInStmt:
		r.hoistVar(s, t.left)
		r.hoistVar(s, t.body)
	case *whileStmt:
		r.hoistVar(s, t.body)
	case *doWhileStmt:
		r.hoistVar(s, t.body)
	case *tryStmt:
		r.hoistVar(s, t.block)
		if t.handler != nil {
			r.hoistVar(s, t.handler)
		}
		if t.finalizer != nil {
			r.hoistVar(s, t.finalizer)
		}
	case *switchStmt:
		for _, c := range t.cases {
			r.hoistVars(s, c.body)
		}
	}
}

func (r *resolver) hoistLexical(s *scope, body []node) {
	for _, st := range body {
		switch t := st.(type) {
		case *varDecl:
			if t.kind == "var" {
				continue
			}
			kind := bLet
			if t.kind == "const" {
				kind = bConst
			}
			for _, d := range t.decls {
				for _, id := range boundNames(d.target, nil) {
					r.declare(s, id.name, kind, id.pos)
				}
			}
		case *funcNode:
			if t.isDecl {
				r.declare(s, t.name, bFunc, t.pos)
				s.funcs = append(s.funcs, t)
			}
		}
	}
}

func (r *resolver) lookup(name string) *binding {
	for s := r.sc; s != nil; s = s.parent {
		if b, ok := s.vars[name]; ok {
			return b
		}
	}
	return nil
}

func (r *resolver) resolveIdent(id *identNode) {
	b := r.lookup(id.name)
	id.ref = r.makeRef(b, id.name)
}

func (r *resolver) makeRef(b *binding, name string) *ref {
	if b == nil || b.global {
		return &ref{b: b, name: name, up: -1}
	}
	check := r.needsCheck(b)
	if b.fn == r.fn {
		return &ref{b: b, name: name, up: -1, check: check}
	}
	b.captured = true
	return &ref{b: b, name: name, up: r.upvalue(r.fn, b), check: check}
}

func (r *resolver) needsCheck(b *binding) bool {
	if r.declaring || (b.kind != bLet && b.kind != bConst && b.kind != bParam) {
		return false
	}
	check := !b.inited || b.always
	for fs := r.fn; !check && fs != nil && fs != b.fn; fs = fs.parent {
		check = fs.node.isDecl
	}
	if check {
		b.tdz = true
	}
	return check
}

func (r *resolver) declPattern(n node) {
	saved := r.declaring
	r.declaring = true
	r.pattern(n)
	r.declaring = saved
	for _, id := range boundNames(n, nil) {
		if b := r.lookup(id.name); b != nil {
			b.inited = true
		}
	}
}

func (r *resolver) exprNotDeclaring(n node) {
	saved := r.declaring
	r.declaring = false
	r.expr(n)
	r.declaring = saved
}

func (r *resolver) upvalue(fs *funcScope, b *binding) int {
	if i, ok := fs.upIndex[b]; ok {
		return i
	}
	if fs.parent != nil && fs.parent != b.fn {
		r.upvalue(fs.parent, b)
	}
	i := len(fs.ups)
	fs.ups = append(fs.ups, b)
	fs.upIndex[b] = i
	return i
}

func (r *resolver) stmts(list []node) {
	for _, s := range list {
		r.stmt(s)
	}
}

func (r *resolver) block(body []node) *scope {
	s := r.pushScope(false)
	r.hoistLexical(s, body)
	r.stmts(body)
	r.popScope()
	return s
}

func (r *resolver) stmt(n node) {
	r.down(n)
	defer func() { r.height-- }()
	switch t := n.(type) {
	case *varDecl:
		for _, d := range t.decls {
			if t.kind == "var" {
				r.pattern(d.target)
				if d.init != nil {
					r.expr(d.init)
				}
				continue
			}
			if d.init != nil {
				r.expr(d.init)
			}
			r.declPattern(d.target)
		}
	case *funcNode:
		r.function(t, false)
	case *exprStmt:
		r.expr(t.expr)
	case *blockStmt:
		t.scope = r.block(t.body)
	case *emptyStmt, *breakStmt, *continueStmt:
	case *returnStmt:
		if t.arg != nil {
			r.expr(t.arg)
		}
	case *throwStmt:
		r.expr(t.arg)
	case *ifStmt:
		r.expr(t.test)
		r.subStmt(t.cons)
		if t.alt != nil {
			r.subStmt(t.alt)
		}
	case *whileStmt:
		r.expr(t.test)
		r.subStmt(t.body)
	case *doWhileStmt:
		r.subStmt(t.body)
		r.expr(t.test)
	case *forStmt:
		t.scope = r.pushScope(false)
		if t.init != nil {
			if d, ok := t.init.(*varDecl); ok {
				r.hoistLexical(t.scope, []node{d})
			}
			r.stmt(t.init)
		}
		if t.test != nil {
			r.expr(t.test)
		}
		if t.update != nil {
			r.expr(t.update)
		}
		r.subStmt(t.body)
		r.popScope()
	case *forInStmt:
		r.expr(t.right)
		t.scope = r.pushScope(false)
		if d, ok := t.left.(*varDecl); ok {
			r.hoistLexical(t.scope, []node{d})
			if d.kind == "var" {
				r.pattern(d.decls[0].target)
			} else {
				r.declPattern(d.decls[0].target)
			}
		} else {
			r.pattern(t.left)
		}
		r.subStmt(t.body)
		r.popScope()
	case *tryStmt:
		t.block.scope = r.block(t.block.body)
		if t.handler != nil {
			t.catchScope = r.pushScope(false)
			if t.param != nil {
				for _, id := range boundNames(t.param, nil) {
					r.declare(t.catchScope, id.name, bCatch, id.pos)
				}
				r.pattern(t.param)
			}
			t.handler.scope = r.block(t.handler.body)
			r.popScope()
		}
		if t.finalizer != nil {
			t.finalizer.scope = r.block(t.finalizer.body)
		}
	case *switchStmt:
		r.expr(t.disc)
		t.scope = r.pushScope(false)
		for _, c := range t.cases {
			r.hoistLexical(t.scope, c.body)
		}
		for _, b := range t.scope.order {
			b.always = true
		}
		for _, c := range t.cases {
			if c.test != nil {
				r.expr(c.test)
			}
			r.stmts(c.body)
		}
		r.popScope()
	default:
		r.fail(n.at(), "unsupported statement")
	}
}

func (r *resolver) subStmt(n node) {
	if b, ok := n.(*blockStmt); ok {
		b.scope = r.block(b.body)
		return
	}
	r.stmt(n)
}

func (r *resolver) pattern(n node) {
	r.down(n)
	defer func() { r.height-- }()
	switch t := n.(type) {
	case *identNode:
		r.resolveIdent(t)
	case *memberNode:
		r.exprNotDeclaring(t)
	case *defaultNode:
		r.pattern(t.target)
		r.exprNotDeclaring(t.value)
	case *restNode:
		r.pattern(t.arg)
	case *arrayPattern:
		for _, e := range t.elems {
			if e != nil {
				r.pattern(e)
			}
		}
	case *objectPattern:
		for _, p := range t.props {
			if p.computed {
				r.exprNotDeclaring(p.key)
			}
			r.pattern(p.value)
		}
		if t.rest != nil {
			r.pattern(t.rest)
		}
	case *varDecl:
		for _, d := range t.decls {
			r.pattern(d.target)
		}
	}
}

func (r *resolver) exprs(list []node) {
	for _, e := range list {
		if e != nil {
			r.expr(e)
		}
	}
}

func (r *resolver) expr(n node) {
	r.down(n)
	defer func() { r.height-- }()
	switch t := n.(type) {
	case *identNode:
		r.resolveIdent(t)
	case *numNode, *strNode, *litNode, *regexNode, *thisNode:
	case *tmplNode:
		if t.tag != nil {
			r.expr(t.tag)
		}
		r.exprs(t.exprs)
	case *arrayNode:
		r.exprs(t.elems)
	case *objectNode:
		for _, p := range t.props {
			if p.computed {
				r.expr(p.key)
			}
			if d, ok := p.value.(*defaultNode); ok && p.shorthand {
				r.fail(d.pos, "Invalid shorthand property initializer")
			}
			r.expr(p.value)
		}
	case *spreadNode:
		r.expr(t.arg)
	case *unaryNode:
		r.expr(t.arg)
	case *updateNode:
		r.expr(t.arg)
	case *binaryNode:
		r.expr(t.left)
		r.expr(t.right)
	case *logicalNode:
		r.expr(t.left)
		r.expr(t.right)
	case *assignNode:
		r.pattern(t.target)
		r.expr(t.value)
	case *condNode:
		r.expr(t.test)
		r.expr(t.cons)
		r.expr(t.alt)
	case *callNode:
		r.expr(t.callee)
		r.exprs(t.args)
	case *newNode:
		r.expr(t.callee)
		r.exprs(t.args)
	case *memberNode:
		r.expr(t.obj)
		if t.prop != nil {
			r.expr(t.prop)
		}
	case *chainNode:
		r.expr(t.expr)
	case *seqNode:
		r.exprs(t.list)
	case *awaitNode:
		r.expr(t.arg)
	case *funcNode:
		r.function(t, false)
	case *defaultNode:
		r.fail(t.pos, "Invalid shorthand property initializer")
	default:
		r.fail(n.at(), "unsupported expression")
	}
}
