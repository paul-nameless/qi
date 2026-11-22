package jsvm

type slotRef struct {
	cell bool
	idx  int
}

type upSrc struct {
	fromUp bool
	idx    int
}

type funcCode struct {
	name       string
	length     int
	nslots     int
	ncells     int
	body       stmt
	exprBody   expr
	arrow      bool
	async      bool
	method     bool
	simple     []slotRef
	params     func(f *frame, args []Value)
	entryCells []int
	tdzSlots   []int
	tdzCells   []int
	self       *slotRef
	hoisted    []hoistFn
	upSrc      []upSrc
	src        *source
	pos        int
	end        int
}

type hoistFn struct {
	make  func(f *frame) Value
	store func(f *frame, v Value)
}

type closure struct {
	code *funcCode
	ups  []*Value
	this Value
}

func (code *funcCode) enter(f *frame, args []Value) {
	for _, i := range code.entryCells {
		f.cells[i] = &Value{r: undefT{}}
	}
	for _, i := range code.tdzCells {
		*f.cells[i] = tdzV
	}
	for _, i := range code.tdzSlots {
		f.slots[i] = tdzV
	}
	if code.self != nil {
		if code.self.cell {
			*f.cells[code.self.idx] = objV(f.fnObj)
		} else {
			f.slots[code.self.idx] = objV(f.fnObj)
		}
	}
	if code.params != nil {
		code.params(f, args)
	} else {
		for i, d := range code.simple {
			v := undefined
			if i < len(args) {
				v = args[i]
			}
			if d.cell {
				*f.cells[d.idx] = v
			} else {
				f.slots[d.idx] = v
			}
		}
	}
	for _, h := range code.hoisted {
		h.store(f, h.make(f))
	}
}

type compiler struct {
	vm    *VM
	fs    *funcScope
	code  *funcCode
	src   *source
	top   bool
	chain int
}

func (c *compiler) fail(pos int, msg string) {
	panic(&syntaxErr{msg: msg, pos: pos})
}

func (vm *VM) compileProgram(src *source, prog *funcNode) (code *funcCode, err error) {
	defer func() {
		if r := recover(); r != nil {
			se, ok := r.(*syntaxErr)
			if !ok {
				panic(r)
			}
			err = se
		}
	}()
	c := &compiler{vm: vm, src: src, top: true}
	return c.function(prog, true), nil
}

func (c *compiler) function(fn *funcNode, top bool) *funcCode {
	fs := fn.scope.fn
	code := &funcCode{name: fn.name, arrow: fn.arrow, async: fn.async, method: fn.method, src: c.src, pos: fn.pos, end: fn.end}
	saved := *c
	c.fs, c.code, c.top = fs, code, top
	for _, p := range fn.params {
		if _, ok := p.(*defaultNode); ok {
			break
		}
		if _, ok := p.(*restNode); ok {
			break
		}
		code.length++
	}
	code.entryCells = c.scopeCells(fn.scope)
	code.tdzSlots, code.tdzCells = tdzOf(fn.scope)
	if fn.selfRef != nil {
		r := c.slotOf(fn.selfRef)
		code.self = &r
		if fn.selfRef.captured {
			code.entryCells = append(code.entryCells, fn.selfRef.idx)
		}
	}
	if fn.simple {
		for _, p := range fn.params {
			b := p.(*identNode).ref.b
			code.simple = append(code.simple, c.slotOf(b))
		}
	} else {
		code.params = c.paramBinder(fn.params)
	}
	code.hoisted = c.hoistFuncs(fn.scope)
	if top {
		code.hoisted = append(c.topVarInit(fn.scope), code.hoisted...)
	}
	if fn.exprBody != nil {
		code.exprBody = c.expr(fn.exprBody)
	} else if top {
		code.body = c.cellBody(fn.body)
	} else if fn.bodyScope != nil {
		code.body = c.block(fn.body, fn.bodyScope)
	} else {
		code.body = c.stmtList(fn.body)
	}
	code.nslots, code.ncells = fs.nslots, fs.ncells
	for _, b := range fs.ups {
		if b.fn == saved.fs {
			code.upSrc = append(code.upSrc, upSrc{false, b.idx})
		} else {
			code.upSrc = append(code.upSrc, upSrc{true, saved.fs.upIndex[b]})
		}
	}
	*c = saved
	return code
}

func (c *compiler) topVarInit(s *scope) []hoistFn {
	var out []hoistFn
	for _, b := range s.order {
		if b.global && b.kind == bVar {
			gb := c.vm.globals.bind(b.name)
			out = append(out, hoistFn{
				make: func(f *frame) Value { return undefined },
				store: func(f *frame, v Value) {
					if !gb.present {
						gb.v, gb.present = undefined, true
					}
					gb.konst = false
				},
			})
		}
	}
	return out
}

func (c *compiler) cellBody(body []node) stmt {
	if len(body) == 0 {
		return func(f *frame) ctl { return ctlNone }
	}
	last := len(body) - 1
	es, ok := body[last].(*exprStmt)
	if !ok {
		return c.stmtList(body)
	}
	head := c.stmtList(body[:last])
	e := c.expr(es.expr)
	return func(f *frame) ctl {
		if r := head(f); r != ctlNone {
			return r
		}
		f.ret = e(f)
		return ctlReturn
	}
}

func (c *compiler) scopeCells(s *scope) []int {
	var cells []int
	for _, b := range s.order {
		if b.captured && !b.global && b.kind != bSelf {
			cells = append(cells, b.idx)
		}
	}
	return cells
}

func tdzOf(s *scope) (slots, cells []int) {
	for _, b := range s.order {
		if !b.tdz || b.global {
			continue
		}
		if b.captured {
			cells = append(cells, b.idx)
		} else {
			slots = append(slots, b.idx)
		}
	}
	return slots, cells
}

func (c *compiler) slotOf(b *binding) slotRef {
	return slotRef{cell: b.captured, idx: b.idx}
}

func (c *compiler) hoistFuncs(s *scope) []hoistFn {
	var out []hoistFn
	for _, fn := range s.funcs {
		b := s.vars[fn.name]
		mk := c.funcExpr(fn)
		st := c.initStore(&ref{b: b, name: b.name, up: -1}, fn.pos, false)
		out = append(out, hoistFn{make: mk, store: st})
	}
	return out
}

func (c *compiler) funcExpr(fn *funcNode) expr {
	code := c.function(fn, false)
	vm := c.vm
	srcs := code.upSrc
	arrow := fn.arrow
	return func(f *frame) Value {
		var ups []*Value
		if len(srcs) > 0 {
			ups = make([]*Value, len(srcs))
			for i, s := range srcs {
				if s.fromUp {
					ups[i] = f.ups[s.idx]
				} else {
					ups[i] = f.cells[s.idx]
				}
			}
		}
		f.vm.track(96)
		cl := &closure{code: code, ups: ups}
		if arrow {
			cl.this = f.this
		}
		return objV(&object{shape: vm.funcRoot, class: classFunction, inner: cl})
	}
}

func (c *compiler) paramBinder(params []node) func(f *frame, args []Value) {
	type pb struct {
		rest   bool
		assign func(f *frame, v Value)
	}
	var list []pb
	for _, p := range params {
		if r, ok := p.(*restNode); ok {
			list = append(list, pb{true, c.pattern(r.arg, true)})
			continue
		}
		list = append(list, pb{false, c.pattern(p, true)})
	}
	vm := c.vm
	return func(f *frame, args []Value) {
		for i, p := range list {
			if p.rest {
				var rest []Value
				if i < len(args) {
					rest = append([]Value(nil), args[i:]...)
				} else {
					rest = []Value{}
				}
				p.assign(f, vm.newArrayV(rest))
				continue
			}
			v := undefined
			if i < len(args) {
				v = args[i]
			}
			p.assign(f, v)
		}
	}
}

func (c *compiler) load(r *ref, pos int) expr {
	b := r.b
	if b == nil || b.global {
		switch r.name {
		case "undefined":
			if b == nil {
				return func(*frame) Value { return undefined }
			}
		case "NaN":
			if b == nil {
				return func(*frame) Value { return vNaN }
			}
		}
		gb := c.vm.globals.bind(r.name)
		name := r.name
		p := int32(pos)
		return func(f *frame) Value {
			if gb.present {
				return gb.v
			}
			f.pos = p
			f.vm.throwError("ReferenceError", name+" is not defined")
			return undefined
		}
	}
	if r.check {
		return c.checkedLoad(r, pos)
	}
	if r.up >= 0 {
		idx := r.up
		return func(f *frame) Value { return *f.ups[idx] }
	}
	idx := b.idx
	if b.captured {
		return func(f *frame) Value { return *f.cells[idx] }
	}
	return func(f *frame) Value { return f.slots[idx] }
}

type tdzT struct{}

var tdzV = Value{r: tdzT{}}

func (v Value) isTDZ() bool { _, ok := v.r.(tdzT); return ok }

func tdzError(f *frame, p int32, name string) {
	f.pos = p
	f.vm.throwError("ReferenceError", "Cannot access '"+name+"' before initialization")
}

func (c *compiler) checkedLoad(r *ref, pos int) expr {
	raw := c.rawLoad(r)
	name, p := r.name, int32(pos)
	return func(f *frame) Value {
		v := raw(f)
		if v.isTDZ() {
			tdzError(f, p, name)
		}
		return v
	}
}

func (c *compiler) rawLoad(r *ref) expr {
	if r.up >= 0 {
		idx := r.up
		return func(f *frame) Value { return *f.ups[idx] }
	}
	idx := r.b.idx
	if r.b.captured {
		return func(f *frame) Value { return *f.cells[idx] }
	}
	return func(f *frame) Value { return f.slots[idx] }
}

func (c *compiler) store(r *ref, pos int) func(f *frame, v Value) {
	b := r.b
	if b != nil && !b.global && (b.kind == bConst || b.kind == bSelf) {
		p := int32(pos)
		var raw expr
		if r.check {
			raw = c.rawLoad(r)
		}
		name := r.name
		return func(f *frame, v Value) {
			if raw != nil && raw(f).isTDZ() {
				tdzError(f, p, name)
			}
			f.pos = p
			f.vm.throwTypeError("Assignment to constant variable.")
		}
	}
	if b != nil && !b.global && r.check {
		raw, st, name, p := c.rawLoad(r), c.rawStore(r), r.name, int32(pos)
		return func(f *frame, v Value) {
			if raw(f).isTDZ() {
				tdzError(f, p, name)
			}
			st(f, v)
		}
	}
	if b == nil || b.global {
		gb := c.vm.globals.bind(r.name)
		p := int32(pos)
		return func(f *frame, v Value) {
			if gb.konst {
				f.pos = p
				f.vm.throwTypeError("Assignment to constant variable.")
			}
			gb.v = v
			gb.present = true
		}
	}
	return c.rawStore(r)
}

func (c *compiler) rawStore(r *ref) func(f *frame, v Value) {
	b := r.b
	if r.up >= 0 {
		idx := r.up
		return func(f *frame, v Value) { *f.ups[idx] = v }
	}
	idx := b.idx
	if b.captured {
		return func(f *frame, v Value) { *f.cells[idx] = v }
	}
	return func(f *frame, v Value) { f.slots[idx] = v }
}

func (c *compiler) initStore(r *ref, pos int, isConst bool) func(f *frame, v Value) {
	b := r.b
	if b == nil {
		return c.store(r, pos)
	}
	if b.global {
		gb := c.vm.globals.bind(r.name)
		return func(f *frame, v Value) {
			gb.v, gb.present, gb.konst = v, true, isConst
		}
	}
	return c.rawStore(r)
}

func (c *compiler) stmtList(list []node) stmt {
	stmts := make([]stmt, 0, len(list))
	for _, n := range list {
		if fn, ok := n.(*funcNode); ok && fn.isDecl {
			continue
		}
		if _, ok := n.(*emptyStmt); ok {
			continue
		}
		stmts = append(stmts, c.stmt(n))
	}
	switch len(stmts) {
	case 0:
		return func(*frame) ctl { return ctlNone }
	case 1:
		return stmts[0]
	case 2:
		a, b := stmts[0], stmts[1]
		return func(f *frame) ctl {
			if r := a(f); r != ctlNone {
				return r
			}
			return b(f)
		}
	}
	return func(f *frame) ctl {
		for _, s := range stmts {
			if r := s(f); r != ctlNone {
				return r
			}
		}
		return ctlNone
	}
}

func (c *compiler) blockEntry(s *scope) func(f *frame) {
	if s == nil {
		return nil
	}
	cells := c.scopeCells(s)
	hoisted := c.hoistFuncs(s)
	tdzSlots, tdzCells := tdzOf(s)
	if len(cells) == 0 && len(hoisted) == 0 && len(tdzSlots) == 0 {
		return nil
	}
	return func(f *frame) {
		for _, i := range cells {
			f.cells[i] = &Value{r: undefT{}}
		}
		for _, i := range tdzCells {
			*f.cells[i] = tdzV
		}
		for _, i := range tdzSlots {
			f.slots[i] = tdzV
		}
		for _, h := range hoisted {
			h.store(f, h.make(f))
		}
	}
}

func (c *compiler) block(body []node, s *scope) stmt {
	entry := c.blockEntry(s)
	list := c.stmtList(body)
	if entry == nil {
		return list
	}
	return func(f *frame) ctl {
		entry(f)
		return list(f)
	}
}

func (c *compiler) stmt(n node) stmt {
	switch t := n.(type) {
	case *exprStmt:
		e := c.exprDiscard(t.expr)
		return func(f *frame) ctl {
			e(f)
			return ctlNone
		}
	case *varDecl:
		return c.varDecl(t)
	case *funcNode:
		return func(*frame) ctl { return ctlNone }
	case *blockStmt:
		return c.block(t.body, t.scope)
	case *emptyStmt:
		return func(*frame) ctl { return ctlNone }
	case *returnStmt:
		if t.arg == nil {
			return func(f *frame) ctl {
				f.ret = undefined
				return ctlReturn
			}
		}
		e := c.expr(t.arg)
		return func(f *frame) ctl {
			f.ret = e(f)
			return ctlReturn
		}
	case *ifStmt:
		return c.ifStmt(t)
	case *forStmt:
		return c.forStmt(t)
	case *forInStmt:
		if t.of {
			return c.forOf(t)
		}
		return c.forIn(t)
	case *whileStmt:
		test := c.cond(t.test)
		body := c.stmt(t.body)
		return func(f *frame) ctl {
			vm := f.vm
			for test(f) {
				switch body(f) {
				case ctlBreak:
					return ctlNone
				case ctlReturn:
					return ctlReturn
				}
				if vm.intr.Load() != 0 {
					panic(&interruptSignal{})
				}
			}
			return ctlNone
		}
	case *doWhileStmt:
		test := c.cond(t.test)
		body := c.stmt(t.body)
		return func(f *frame) ctl {
			vm := f.vm
			for {
				switch body(f) {
				case ctlBreak:
					return ctlNone
				case ctlReturn:
					return ctlReturn
				}
				if vm.intr.Load() != 0 {
					panic(&interruptSignal{})
				}
				if !test(f) {
					return ctlNone
				}
			}
		}
	case *breakStmt:
		return func(*frame) ctl { return ctlBreak }
	case *continueStmt:
		return func(*frame) ctl { return ctlContinue }
	case *throwStmt:
		e := c.expr(t.arg)
		p := int32(t.pos)
		return func(f *frame) ctl {
			v := e(f)
			f.pos = p
			panic(&jsThrow{v})
		}
	case *tryStmt:
		return c.tryStmt(t)
	case *switchStmt:
		return c.switchStmt(t)
	}
	c.fail(n.at(), "unsupported statement")
	return nil
}

func (c *compiler) varDecl(d *varDecl) stmt {
	var parts []func(f *frame)
	isConst := d.kind == "const"
	for _, dc := range d.decls {
		if dc.init == nil {
			if d.kind == "var" {
				continue
			}
			id := dc.target.(*identNode)
			st := c.initStore(id.ref, id.pos, false)
			parts = append(parts, func(f *frame) { st(f, undefined) })
			continue
		}
		init := c.namedExpr(dc.init, dc.target)
		if id, ok := dc.target.(*identNode); ok {
			var st func(*frame, Value)
			if d.kind == "var" {
				st = c.store(id.ref, id.pos)
			} else {
				st = c.initStore(id.ref, id.pos, isConst)
			}
			parts = append(parts, func(f *frame) { st(f, init(f)) })
			continue
		}
		pat := c.patternDecl(dc.target, d.kind)
		parts = append(parts, func(f *frame) { pat(f, init(f)) })
	}
	if len(parts) == 1 {
		p := parts[0]
		return func(f *frame) ctl {
			p(f)
			return ctlNone
		}
	}
	return func(f *frame) ctl {
		for _, p := range parts {
			p(f)
		}
		return ctlNone
	}
}

func (c *compiler) namedExpr(n node, target node) expr {
	if fn, ok := n.(*funcNode); ok && fn.name == "" {
		if id, ok := target.(*identNode); ok {
			fn.name = id.name
			e := c.expr(fn)
			fn.name = ""
			return e
		}
	}
	return c.expr(n)
}

func (c *compiler) ifStmt(t *ifStmt) stmt {
	test := c.cond(t.test)
	cons := c.stmt(t.cons)
	if t.alt == nil {
		return func(f *frame) ctl {
			if test(f) {
				return cons(f)
			}
			return ctlNone
		}
	}
	alt := c.stmt(t.alt)
	return func(f *frame) ctl {
		if test(f) {
			return cons(f)
		}
		return alt(f)
	}
}

func (c *compiler) forStmt(t *forStmt) stmt {
	entry := c.blockEntry(t.scope)
	var perIter []int
	if t.scope != nil {
		perIter = c.scopeCells(t.scope)
	}
	var init stmt
	if t.init != nil {
		init = c.stmt(t.init)
	}
	var test cond
	if t.test != nil {
		test = c.cond(t.test)
	}
	var update expr
	if t.update != nil {
		update = c.exprDiscard(t.update)
	}
	body := c.stmt(t.body)
	return func(f *frame) ctl {
		vm := f.vm
		if entry != nil {
			entry(f)
		}
		if init != nil {
			init(f)
		}
		for test == nil || test(f) {
			switch body(f) {
			case ctlBreak:
				return ctlNone
			case ctlReturn:
				return ctlReturn
			}
			for _, i := range perIter {
				nb := new(Value)
				*nb = *f.cells[i]
				f.cells[i] = nb
			}
			if update != nil {
				update(f)
			}
			if vm.intr.Load() != 0 {
				panic(&interruptSignal{})
			}
		}
		return ctlNone
	}
}

func (c *compiler) loopTarget(left node) func(f *frame, v Value) {
	if d, ok := left.(*varDecl); ok {
		target := d.decls[0].target
		var assign func(f *frame, v Value)
		if id, ok := target.(*identNode); ok {
			if d.kind == "var" {
				assign = c.store(id.ref, id.pos)
			} else {
				assign = c.initStore(id.ref, id.pos, d.kind == "const")
			}
		} else {
			assign = c.patternDecl(target, d.kind)
		}
		return assign
	}
	return c.pattern(left, false)
}

func (c *compiler) forOf(t *forInStmt) stmt {
	right := c.expr(t.right)
	entry := c.blockEntry(t.scope)
	assign := c.loopTarget(t.left)
	body := c.stmt(t.body)
	p := int32(t.right.at())
	return func(f *frame) ctl {
		vm := f.vm
		iterable := right(f)
		result := ctlNone
		f.pos = p
		vm.iterate(iterable, func(v Value) bool {
			if entry != nil {
				entry(f)
			}
			assign(f, v)
			switch body(f) {
			case ctlBreak:
				return false
			case ctlReturn:
				result = ctlReturn
				return false
			}
			if vm.intr.Load() != 0 {
				panic(&interruptSignal{})
			}
			return true
		})
		return result
	}
}

func (c *compiler) forIn(t *forInStmt) stmt {
	right := c.expr(t.right)
	entry := c.blockEntry(t.scope)
	assign := c.loopTarget(t.left)
	body := c.stmt(t.body)
	return func(f *frame) ctl {
		vm := f.vm
		ov := right(f)
		o, ok := ov.r.(*object)
		var keys []propKey
		if ok {
			keys = vm.forInKeys(o)
		} else if s, isStr := ov.str(); isStr {
			o = vm.toObject(strVal(s))
			keys = o.ownKeys(true)
		}
		for _, k := range keys {
			if !vm.hasProperty(o, k) {
				continue
			}
			if entry != nil {
				entry(f)
			}
			assign(f, strV(k.str()))
			switch body(f) {
			case ctlBreak:
				return ctlNone
			case ctlReturn:
				return ctlReturn
			}
			if vm.intr.Load() != 0 {
				panic(&interruptSignal{})
			}
		}
		return ctlNone
	}
}

func (c *compiler) tryStmt(t *tryStmt) stmt {
	block := c.block(t.block.body, t.block.scope)
	var handler stmt
	var bind func(f *frame, v Value)
	var catchEntry func(f *frame)
	if t.handler != nil {
		handler = c.block(t.handler.body, t.handler.scope)
		catchEntry = c.blockEntry(t.catchScope)
		if t.param != nil {
			bind = c.patternDecl(t.param, "let")
		}
	}
	var final stmt
	if t.finalizer != nil {
		final = c.block(t.finalizer.body, t.finalizer.scope)
	}
	return func(f *frame) ctl {
		r, exc := runTry(f, block)
		if exc != nil && handler != nil {
			h := func(f *frame) ctl {
				if catchEntry != nil {
					catchEntry(f)
				}
				if bind != nil {
					bind(f, exc.v)
				}
				return handler(f)
			}
			if final == nil {
				return h(f)
			}
			r, exc = runTry(f, h)
		}
		if final != nil {
			saved := f.ret
			if fr := final(f); fr != ctlNone {
				return fr
			}
			f.ret = saved
		}
		if exc != nil {
			panic(exc)
		}
		return r
	}
}

func runTry(f *frame, body stmt) (r ctl, exc *jsThrow) {
	vm := f.vm
	sp, depth := vm.sp, vm.depth
	defer func() {
		if p := recover(); p != nil {
			t, ok := vm.asThrow(p)
			if !ok {
				panic(p)
			}
			vm.sp, vm.depth, vm.top = sp, depth, f
			exc = t
		}
	}()
	return body(f), nil
}

func (c *compiler) switchStmt(t *switchStmt) stmt {
	disc := c.expr(t.disc)
	entry := c.blockEntry(t.scope)
	tests := make([]expr, len(t.cases))
	bodies := make([]stmt, len(t.cases))
	def := -1
	for i, cs := range t.cases {
		if cs.test == nil {
			def = i
		} else {
			tests[i] = c.expr(cs.test)
		}
		bodies[i] = c.stmtList(cs.body)
	}
	return func(f *frame) ctl {
		v := disc(f)
		if entry != nil {
			entry(f)
		}
		start := -1
		for i, te := range tests {
			if te != nil && strictEquals(v, te(f)) {
				start = i
				break
			}
		}
		if start < 0 {
			start = def
		}
		if start < 0 {
			return ctlNone
		}
		for _, b := range bodies[start:] {
			switch r := b(f); r {
			case ctlNone:
			case ctlBreak:
				return ctlNone
			default:
				return r
			}
		}
		return ctlNone
	}
}
