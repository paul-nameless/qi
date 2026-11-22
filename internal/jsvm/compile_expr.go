package jsvm

import (
	"math"
)

type shortT struct{}

var shortV = Value{r: shortT{}}

func (v Value) isShort() bool { _, ok := v.r.(shortT); return ok }

func constExpr(v Value) expr { return func(*frame) Value { return v } }

func (c *compiler) exprDiscard(n node) expr {
	if u, ok := n.(*updateNode); ok && !u.prefix {
		cp := *u
		cp.prefix = true
		return c.expr(&cp)
	}
	return c.expr(n)
}

func (c *compiler) expr(n node) expr {
	switch t := n.(type) {
	case *identNode:
		return c.load(t.ref, t.pos)
	case *numNode:
		return constExpr(num(t.v))
	case *strNode:
		return constExpr(strV(t.v))
	case *litNode:
		return constExpr(t.v)
	case *thisNode:
		return func(f *frame) Value { return f.this }
	case *tmplNode:
		return c.template(t)
	case *regexNode:
		return c.regexLit(t)
	case *arrayNode:
		return c.arrayLit(t)
	case *objectNode:
		return c.objectLit(t)
	case *funcNode:
		return c.funcExpr(t)
	case *unaryNode:
		return c.unary(t)
	case *updateNode:
		return c.update(t)
	case *binaryNode:
		return c.binary(t)
	case *logicalNode:
		return c.logical(t)
	case *assignNode:
		return c.assign(t)
	case *condNode:
		test, cons, alt := c.cond(t.test), c.expr(t.cons), c.expr(t.alt)
		return func(f *frame) Value {
			if test(f) {
				return cons(f)
			}
			return alt(f)
		}
	case *callNode:
		return c.call(t)
	case *newNode:
		return c.newExpr(t)
	case *memberNode:
		return c.member(t)
	case *chainNode:
		c.chain++
		inner := c.expr(t.expr)
		c.chain--
		return func(f *frame) Value {
			v := inner(f)
			if v.isShort() {
				return undefined
			}
			return v
		}
	case *seqNode:
		list := make([]expr, len(t.list))
		for i, e := range t.list {
			list[i] = c.expr(e)
		}
		return func(f *frame) Value {
			var v Value
			for _, e := range list {
				v = e(f)
			}
			return v
		}
	case *awaitNode:
		arg := c.expr(t.arg)
		p := int32(t.pos)
		return func(f *frame) Value {
			v := arg(f)
			f.pos = p
			return f.vm.await(f, v)
		}
	case *spreadNode:
		c.fail(t.pos, "Unexpected token '...'")
	}
	c.fail(n.at(), "unsupported expression")
	return nil
}

func (c *compiler) template(t *tmplNode) expr {
	quasis := t.quasis
	if t.tag != nil {
		quasis = t.raws
	}
	exprs := make([]expr, len(t.exprs))
	for i, e := range t.exprs {
		exprs[i] = c.expr(e)
	}
	if len(exprs) == 0 {
		return constExpr(strV(quasis[0]))
	}
	return func(f *frame) Value {
		b := sbuf{vm: f.vm}
		b.add(quasis[0])
		for i, e := range exprs {
			b.add(f.vm.toString(e(f)))
			b.add(quasis[i+1])
		}
		return b.value()
	}
}

func (c *compiler) regexLit(t *regexNode) expr {
	re, err := compileRegex(t.pattern, t.flags)
	if err != nil {
		c.fail(t.pos, err.Error())
	}
	return func(f *frame) Value { return objV(f.vm.newRegExpObj(re)) }
}

func (c *compiler) arrayLit(t *arrayNode) expr {
	hasSpread, holey := false, false
	elems := make([]expr, len(t.elems))
	spread := make([]bool, len(t.elems))
	for i, e := range t.elems {
		if e == nil {
			elems[i] = constExpr(holeV)
			holey = true
			continue
		}
		if s, ok := e.(*spreadNode); ok {
			hasSpread = true
			spread[i] = true
			elems[i] = c.expr(s.arg)
			continue
		}
		elems[i] = c.expr(e)
	}
	if !hasSpread {
		return func(f *frame) Value {
			vals := make([]Value, len(elems))
			for i, e := range elems {
				vals[i] = e(f)
			}
			a := f.vm.newArray(vals)
			a.holes = holey
			return objV(a)
		}
	}
	return func(f *frame) Value {
		vals := make([]Value, 0, len(elems))
		vm := f.vm
		for i, e := range elems {
			v := e(f)
			if spread[i] {
				if o, ok := v.r.(*object); ok && o.class == classArray {
					vals = vm.appendV(vals, dense(o)...)
					continue
				}
				vm.iterate(v, func(x Value) bool {
					vals = vm.appendV(vals, x)
					return true
				})
				continue
			}
			vals = append(vals, v)
		}
		a := vm.newArray(vals)
		a.holes = holey
		return objV(a)
	}
}

func (c *compiler) propKeyName(p *propNode) string {
	if s, ok := p.key.(*strNode); ok {
		return s.v
	}
	return ""
}

func (c *compiler) objectLit(t *objectNode) expr {
	static := true
	seen := map[string]bool{}
	for _, p := range t.props {
		if p.spread || p.computed {
			static = false
			break
		}
		k := c.propKeyName(p)
		if seen[k] || k == "__proto__" {
			static = false
			break
		}
		seen[k] = true
	}
	if static {
		return c.staticObject(t)
	}
	type part struct {
		key    expr
		name   propKey
		value  expr
		spread bool
		proto  bool
	}
	parts := make([]part, len(t.props))
	for i, p := range t.props {
		switch {
		case p.spread:
			parts[i] = part{spread: true, value: c.expr(p.value)}
		case p.computed:
			parts[i] = part{key: c.expr(p.key), value: c.expr(p.value)}
		default:
			name := c.propKeyName(p)
			parts[i] = part{name: strKey(name), value: c.propValue(p, name), proto: name == "__proto__" && !p.shorthand && !p.method}
		}
	}
	return func(f *frame) Value {
		vm := f.vm
		o := vm.newObject()
		vm.track(80 + 32*len(parts))
		for _, p := range parts {
			switch {
			case p.spread:
				vm.copyDataProps(o, p.value(f), nil)
			case p.key != nil:
				k := vm.toPropertyKey(p.key(f))
				vm.defineOwn(o, k, p.value(f))
			case p.proto:
				v := p.value(f)
				if po, ok := v.r.(*object); ok {
					o.setProto(po)
				} else if v.isNull() {
					o.setProto(nil)
				}
			default:
				vm.defineOwn(o, p.name, p.value(f))
			}
		}
		return objV(o)
	}
}

func (c *compiler) propValue(p *propNode, name string) expr {
	if fn, ok := p.value.(*funcNode); ok && fn.name == "" {
		fn.name = name
		e := c.expr(fn)
		fn.name = ""
		return e
	}
	return c.expr(p.value)
}

func (c *compiler) staticObject(t *objectNode) expr {
	shape := c.vm.objectRoot
	vals := make([]expr, len(t.props))
	for i, p := range t.props {
		name := c.propKeyName(p)
		shape = shape.withProp(strKey(name), fDefault)
		vals[i] = c.propValue(p, name)
	}
	if len(t.props) > dictMax {
		shape = nil
	}
	n := len(vals)
	if shape == nil {
		keys := make([]propKey, n)
		for i, p := range t.props {
			keys[i] = strKey(c.propKeyName(p))
		}
		return func(f *frame) Value {
			o := f.vm.newObject()
			for i, e := range vals {
				o.setOwn(keys[i], e(f))
			}
			return objV(o)
		}
	}
	size := 80 + 24*n
	return func(f *frame) Value {
		slots := make([]Value, n)
		for i, e := range vals {
			slots[i] = e(f)
		}
		f.vm.track(size)
		return objV(&object{shape: shape, slots: slots})
	}
}

func (c *compiler) unary(t *unaryNode) expr {
	if t.op == "typeof" {
		if id, ok := t.arg.(*identNode); ok && (id.ref.b == nil || id.ref.b.global) {
			gb := c.vm.globals.bind(id.ref.name)
			if id.ref.name == "undefined" && id.ref.b == nil {
				return constExpr(strV("undefined"))
			}
			return func(f *frame) Value {
				if !gb.present {
					return strV("undefined")
				}
				return Value{r: typeofStr(gb.v)}
			}
		}
		arg := c.expr(t.arg)
		return func(f *frame) Value { return Value{r: typeofStr(arg(f))} }
	}
	if t.op == "delete" {
		return c.deleteExpr(t)
	}
	if t.op == "!" {
		cnd := c.cond(t.arg)
		return func(f *frame) Value { return boolV(!cnd(f)) }
	}
	arg := c.expr(t.arg)
	switch t.op {
	case "-":
		if n, ok := t.arg.(*numNode); ok {
			return constExpr(num(-n.v))
		}
		return func(f *frame) Value {
			v := arg(f)
			if v.r == nil {
				return Value{n: -v.n}
			}
			return num(-f.vm.toNumber(v))
		}
	case "+":
		return func(f *frame) Value {
			v := arg(f)
			if v.r == nil {
				return v
			}
			return num(f.vm.toNumber(v))
		}
	case "~":
		return func(f *frame) Value { return num(float64(^toInt32(f.vm.toNumber(arg(f))))) }
	case "void":
		return func(f *frame) Value {
			arg(f)
			return undefined
		}
	}
	c.fail(t.pos, "unsupported operator "+t.op)
	return nil
}

var typeofStrs = map[string]*jstr{}

func typeofStr(v Value) *jstr {
	s := typeOf(v)
	if p, ok := typeofStrs[s]; ok {
		return p
	}
	return newASCII(s)
}

func init() {
	for _, s := range []string{"undefined", "object", "boolean", "number", "string", "symbol", "function", "bigint"} {
		typeofStrs[s] = &jstr{s: s, n: len(s), info: strKnown | strASCII | strShared}
	}
}

func (c *compiler) deleteExpr(t *unaryNode) expr {
	m, ok := t.arg.(*memberNode)
	if !ok {
		arg := c.expr(t.arg)
		return func(f *frame) Value {
			arg(f)
			return vTrue
		}
	}
	obj := c.expr(m.obj)
	key := c.memberKey(m)
	p := int32(m.pos)
	return func(f *frame) Value {
		ov := obj(f)
		k := key(f)
		f.pos = p
		return boolV(f.vm.deleteProp(ov, k))
	}
}

func (c *compiler) memberKey(m *memberNode) func(f *frame) propKey {
	if m.prop == nil {
		k := strKey(m.name)
		return func(*frame) propKey { return k }
	}
	e := c.expr(m.prop)
	return func(f *frame) propKey { return f.vm.toPropertyKey(e(f)) }
}

func (c *compiler) update(t *updateNode) expr {
	delta := 1.0
	if t.op == "--" {
		delta = -1
	}
	prefix := t.prefix
	if id, ok := t.arg.(*identNode); ok {
		r := id.ref
		if r.b != nil && !r.b.global && r.up < 0 && !r.b.captured && r.b.kind != bConst && !r.check {
			idx := r.b.idx
			return func(f *frame) Value {
				s := &f.slots[idx]
				if s.r == nil {
					old := s.n
					s.n += delta
					if prefix {
						return Value{n: s.n}
					}
					return Value{n: old}
				}
				old := f.vm.toNumeric(*s)
				f.slots[idx] = num(old + delta)
				if prefix {
					return num(old + delta)
				}
				return num(old)
			}
		}
		load, store := c.load(r, id.pos), c.store(r, id.pos)
		return func(f *frame) Value {
			v := load(f)
			var old float64
			if v.r == nil {
				old = v.n
			} else {
				old = f.vm.toNumeric(v)
			}
			store(f, num(old+delta))
			if prefix {
				return num(old + delta)
			}
			return num(old)
		}
	}
	m := t.arg.(*memberNode)
	obj := c.expr(m.obj)
	key := c.memberKey(m)
	p := int32(m.pos)
	return func(f *frame) Value {
		ov := obj(f)
		k := key(f)
		f.pos = p
		vm := f.vm
		old := vm.toNumeric(vm.getV(ov, k))
		vm.setV(ov, k, num(old+delta))
		if prefix {
			return num(old + delta)
		}
		return num(old)
	}
}

func (c *compiler) logical(t *logicalNode) expr {
	l, r := c.expr(t.left), c.expr(t.right)
	switch t.op {
	case "&&":
		return func(f *frame) Value {
			v := l(f)
			if !toBool(v) {
				return v
			}
			return r(f)
		}
	case "||":
		return func(f *frame) Value {
			v := l(f)
			if toBool(v) {
				return v
			}
			return r(f)
		}
	}
	return func(f *frame) Value {
		v := l(f)
		if !v.isNullish() {
			return v
		}
		return r(f)
	}
}

func (c *compiler) cond(n node) cond {
	switch t := n.(type) {
	case *binaryNode:
		if cf := c.compareCond(t); cf != nil {
			return cf
		}
	case *logicalNode:
		if t.op != "??" {
			l, r := c.cond(t.left), c.cond(t.right)
			if t.op == "&&" {
				return func(f *frame) bool { return l(f) && r(f) }
			}
			return func(f *frame) bool { return l(f) || r(f) }
		}
	case *unaryNode:
		if t.op == "!" {
			inner := c.cond(t.arg)
			return func(f *frame) bool { return !inner(f) }
		}
	case *litNode:
		b := toBool(t.v)
		return func(*frame) bool { return b }
	}
	e := c.expr(n)
	return func(f *frame) bool {
		v := e(f)
		if _, ok := v.r.(boolT); ok {
			return v.n != 0
		}
		return toBool(v)
	}
}

func (c *compiler) slotOperand(n node) (int, bool) {
	id, ok := n.(*identNode)
	if !ok || id.ref == nil {
		return 0, false
	}
	b := id.ref.b
	if b == nil || b.global || b.captured || id.ref.up >= 0 || id.ref.check {
		return 0, false
	}
	return b.idx, true
}

func (c *compiler) compareCond(t *binaryNode) cond {
	var cmp func(a, b float64) bool
	switch t.op {
	case "<":
		cmp = func(a, b float64) bool { return a < b }
	case "<=":
		cmp = func(a, b float64) bool { return a <= b }
	case ">":
		cmp = func(a, b float64) bool { return a > b }
	case ">=":
		cmp = func(a, b float64) bool { return a >= b }
	case "===", "!==":
		l, r := c.expr(t.left), c.expr(t.right)
		neg := t.op == "!=="
		return func(f *frame) bool { return strictEquals(l(f), r(f)) != neg }
	case "==", "!=":
		l, r := c.expr(t.left), c.expr(t.right)
		neg := t.op == "!="
		return func(f *frame) bool { return f.vm.looseEquals(l(f), r(f)) != neg }
	default:
		return nil
	}
	op := t.op
	if idx, ok := c.slotOperand(t.left); ok {
		if n, ok := t.right.(*numNode); ok {
			k := n.v
			switch op {
			case "<":
				return func(f *frame) bool {
					v := f.slots[idx]
					if v.r == nil {
						return v.n < k
					}
					return f.vm.relational(op, v, num(k))
				}
			case "<=":
				return func(f *frame) bool {
					v := f.slots[idx]
					if v.r == nil {
						return v.n <= k
					}
					return f.vm.relational(op, v, num(k))
				}
			}
		}
	}
	l, r := c.expr(t.left), c.expr(t.right)
	return func(f *frame) bool {
		a, b := l(f), r(f)
		if a.r == nil && b.r == nil {
			return cmp(a.n, b.n)
		}
		return f.vm.relational(op, a, b)
	}
}

func jsMod(a, b float64) float64 {
	if a > 0 && b > 0 && a <= 9007199254740991 && b <= 9007199254740991 {
		ai, bi := int64(a), int64(b)
		if float64(ai) == a && float64(bi) == b {
			return float64(ai % bi)
		}
	}
	if b == 0 || math.IsInf(a, 0) || a != a || b != b {
		return math.NaN()
	}
	if math.IsInf(b, 0) {
		return a
	}
	r := math.Mod(a, b)
	if r == 0 && math.Signbit(a) {
		return math.Copysign(0, -1)
	}
	return r
}

func (c *compiler) binary(t *binaryNode) expr {
	switch t.op {
	case "<", "<=", ">", ">=", "===", "!==", "==", "!=":
		cf := c.compareCond(t)
		return func(f *frame) Value { return boolV(cf(f)) }
	}
	if idx, ok := c.slotOperand(t.left); ok {
		if n, ok := t.right.(*numNode); ok {
			if e := c.slotConstOp(t.op, idx, n.v); e != nil {
				return e
			}
		}
	}
	l, r := c.expr(t.left), c.expr(t.right)
	p := int32(t.pos)
	switch t.op {
	case "+":
		return func(f *frame) Value {
			a, b := l(f), r(f)
			if a.r == nil && b.r == nil {
				return Value{n: a.n + b.n}
			}
			return f.vm.add(a, b)
		}
	case "-":
		return func(f *frame) Value {
			a, b := l(f), r(f)
			if a.r == nil && b.r == nil {
				return Value{n: a.n - b.n}
			}
			return Value{n: f.vm.toNumeric(a) - f.vm.toNumeric(b)}
		}
	case "*":
		return func(f *frame) Value {
			a, b := l(f), r(f)
			if a.r == nil && b.r == nil {
				return Value{n: a.n * b.n}
			}
			return Value{n: f.vm.toNumeric(a) * f.vm.toNumeric(b)}
		}
	case "/":
		return func(f *frame) Value {
			a, b := l(f), r(f)
			if a.r == nil && b.r == nil {
				return Value{n: a.n / b.n}
			}
			return Value{n: f.vm.toNumeric(a) / f.vm.toNumeric(b)}
		}
	case "%":
		return func(f *frame) Value {
			a, b := l(f), r(f)
			if a.r == nil && b.r == nil {
				return Value{n: jsMod(a.n, b.n)}
			}
			return Value{n: jsMod(f.vm.toNumeric(a), f.vm.toNumeric(b))}
		}
	case "**":
		return func(f *frame) Value {
			a, b := l(f), r(f)
			return Value{n: jsPow(f.vm.toNumeric(a), f.vm.toNumeric(b))}
		}
	case "&", "|", "^", "<<", ">>", ">>>":
		op := intOp(t.op)
		return func(f *frame) Value {
			a, b := l(f), r(f)
			return Value{n: op(f.vm.toNumeric(a), f.vm.toNumeric(b))}
		}
	case "instanceof":
		return func(f *frame) Value {
			a, b := l(f), r(f)
			f.pos = p
			return boolV(f.vm.instanceOf(a, b))
		}
	case "in":
		return func(f *frame) Value {
			a, b := l(f), r(f)
			o, ok := b.r.(*object)
			if !ok {
				f.pos = p
				f.vm.throwTypeError("Cannot use 'in' operator to search for '" + f.vm.toString(a) + "' in " + ToString(b))
			}
			return boolV(f.vm.hasProperty(o, f.vm.toPropertyKey(a)))
		}
	}
	c.fail(t.pos, "unsupported operator "+t.op)
	return nil
}

func (c *compiler) slotConstOp(op string, idx int, k float64) expr {
	switch op {
	case "+":
		return func(f *frame) Value {
			v := f.slots[idx]
			if v.r == nil {
				return Value{n: v.n + k}
			}
			return f.vm.add(v, num(k))
		}
	case "-":
		return func(f *frame) Value {
			v := f.slots[idx]
			if v.r == nil {
				return Value{n: v.n - k}
			}
			return Value{n: f.vm.toNumeric(v) - k}
		}
	case "*":
		return func(f *frame) Value {
			v := f.slots[idx]
			if v.r == nil {
				return Value{n: v.n * k}
			}
			return Value{n: f.vm.toNumeric(v) * k}
		}
	case "%":
		return func(f *frame) Value {
			v := f.slots[idx]
			if v.r == nil {
				return Value{n: jsMod(v.n, k)}
			}
			return Value{n: jsMod(f.vm.toNumeric(v), k)}
		}
	}
	return nil
}

func intOp(op string) func(a, b float64) float64 {
	switch op {
	case "&":
		return func(a, b float64) float64 { return float64(toInt32(a) & toInt32(b)) }
	case "|":
		return func(a, b float64) float64 { return float64(toInt32(a) | toInt32(b)) }
	case "^":
		return func(a, b float64) float64 { return float64(toInt32(a) ^ toInt32(b)) }
	case "<<":
		return func(a, b float64) float64 { return float64(toInt32(a) << (toUint32(b) & 31)) }
	case ">>":
		return func(a, b float64) float64 { return float64(toInt32(a) >> (toUint32(b) & 31)) }
	}
	return func(a, b float64) float64 { return float64(toUint32(a) >> (toUint32(b) & 31)) }
}

func jsPow(a, b float64) float64 {
	if b != b {
		return math.NaN()
	}
	if b == 0 {
		return 1
	}
	if (a == 1 || a == -1) && math.IsInf(b, 0) {
		return math.NaN()
	}
	return math.Pow(a, b)
}

func compoundOp(op string) func(vm *VM, a, b Value) Value {
	switch op {
	case "+=":
		return func(vm *VM, a, b Value) Value {
			if a.r == nil && b.r == nil {
				return Value{n: a.n + b.n}
			}
			return vm.add(a, b)
		}
	case "-=":
		return func(vm *VM, a, b Value) Value { return num(vm.toNumeric(a) - vm.toNumeric(b)) }
	case "*=":
		return func(vm *VM, a, b Value) Value { return num(vm.toNumeric(a) * vm.toNumeric(b)) }
	case "/=":
		return func(vm *VM, a, b Value) Value { return num(vm.toNumeric(a) / vm.toNumeric(b)) }
	case "%=":
		return func(vm *VM, a, b Value) Value { return num(jsMod(vm.toNumeric(a), vm.toNumeric(b))) }
	case "**=":
		return func(vm *VM, a, b Value) Value { return num(jsPow(vm.toNumeric(a), vm.toNumeric(b))) }
	}
	iop := intOp(op[:len(op)-1])
	return func(vm *VM, a, b Value) Value { return num(iop(vm.toNumeric(a), vm.toNumeric(b))) }
}

func (c *compiler) assign(t *assignNode) expr {
	switch t.op {
	case "=":
		val := c.namedExpr(t.value, t.target)
		switch tg := t.target.(type) {
		case *identNode:
			st := c.store(tg.ref, tg.pos)
			return func(f *frame) Value {
				v := val(f)
				st(f, v)
				return v
			}
		case *memberNode:
			return c.memberAssign(tg, val)
		}
		pat := c.pattern(t.target, false)
		return func(f *frame) Value {
			v := val(f)
			pat(f, v)
			return v
		}
	case "&&=", "||=", "??=":
		return c.logicalAssign(t)
	}
	val := c.expr(t.value)
	op := compoundOp(t.op)
	if id, ok := t.target.(*identNode); ok {
		if idx, ok := c.slotOperand(id); ok && t.op == "+=" && id.ref.b.kind != bConst {
			return func(f *frame) Value {
				a := f.slots[idx]
				b := val(f)
				if a.r == nil && b.r == nil {
					nv := Value{n: a.n + b.n}
					f.slots[idx] = nv
					return nv
				}
				nv := f.vm.add(a, b)
				f.slots[idx] = nv
				return nv
			}
		}
		if id.ref.b == nil || id.ref.b.global {
			if t.op == "+=" {
				gb := c.vm.globals.bind(id.ref.name)
				load, store := c.load(id.ref, id.pos), c.store(id.ref, id.pos)
				return func(f *frame) Value {
					var a Value
					if gb.present {
						a = gb.v
					} else {
						a = load(f)
					}
					b := val(f)
					if a.r == nil && b.r == nil && gb.present && !gb.konst {
						gb.v = Value{n: a.n + b.n}
						return gb.v
					}
					nv := f.vm.add(a, b)
					store(f, nv)
					return nv
				}
			}
		}
		load, store := c.load(id.ref, id.pos), c.store(id.ref, id.pos)
		return func(f *frame) Value {
			a := load(f)
			nv := op(f.vm, a, val(f))
			store(f, nv)
			return nv
		}
	}
	m := t.target.(*memberNode)
	obj := c.expr(m.obj)
	key := c.memberKey(m)
	p := int32(m.pos)
	return func(f *frame) Value {
		ov := obj(f)
		k := key(f)
		vm := f.vm
		f.pos = p
		a := vm.getV(ov, k)
		nv := op(vm, a, val(f))
		f.pos = p
		vm.setV(ov, k, nv)
		return nv
	}
}

func (c *compiler) logicalAssign(t *assignNode) expr {
	val := c.namedExpr(t.value, t.target)
	test := func(v Value) bool { return toBool(v) }
	switch t.op {
	case "&&=":
		test = func(v Value) bool { return !toBool(v) }
	case "??=":
		test = func(v Value) bool { return !v.isNullish() }
	}
	if id, ok := t.target.(*identNode); ok {
		load, store := c.load(id.ref, id.pos), c.store(id.ref, id.pos)
		return func(f *frame) Value {
			a := load(f)
			if test(a) {
				return a
			}
			v := val(f)
			store(f, v)
			return v
		}
	}
	m := t.target.(*memberNode)
	obj := c.expr(m.obj)
	key := c.memberKey(m)
	p := int32(m.pos)
	return func(f *frame) Value {
		ov := obj(f)
		k := key(f)
		f.pos = p
		a := f.vm.getV(ov, k)
		if test(a) {
			return a
		}
		v := val(f)
		f.pos = p
		f.vm.setV(ov, k, v)
		return v
	}
}

func (c *compiler) memberAssign(m *memberNode, val expr) expr {
	obj := c.expr(m.obj)
	p := int32(m.pos)
	if m.prop == nil {
		key := strKey(m.name)
		ic := &setIC{}
		return func(f *frame) Value {
			ov := obj(f)
			v := val(f)
			if o, ok := ov.r.(*object); ok {
				if o.shape == ic.shape {
					o.slots[ic.idx] = v
					return v
				}
				if o.shape == ic.from && !o.noExt && o.class == classObject {
					o.shape = ic.to
					o.slots = append(o.slots, v)
					return v
				}
				f.pos = p
				f.vm.setCached(o, key, v, ic)
				return v
			}
			f.pos = p
			f.vm.setV(ov, key, v)
			return v
		}
	}
	prop := c.expr(m.prop)
	return func(f *frame) Value {
		ov := obj(f)
		kv := prop(f)
		v := val(f)
		if o, ok := ov.r.(*object); ok && o.class == classArray && kv.r == nil {
			i := int(kv.n)
			if float64(i) == kv.n && i >= 0 && !o.noExt {
				if i < len(o.arr) {
					o.arr[i] = v
					return v
				}
				if i == len(o.arr) {
					o.arr = append(o.arr, v)
					f.vm.track(24)
					return v
				}
			}
		}
		f.pos = p
		f.vm.setV(ov, f.vm.toPropertyKey(kv), v)
		return v
	}
}

type getIC struct {
	shape  *hclass
	idx    int
	holder *object
	hshape *hclass
}

type setIC struct {
	shape *hclass
	idx   int
	from  *hclass
	to    *hclass
}

func (c *compiler) member(m *memberNode) expr {
	inChain := c.chain > 0
	obj := c.expr(m.obj)
	p := int32(m.pos)
	optional := m.optional
	if m.prop == nil {
		return c.namedGet(obj, m.name, p, inChain, optional)
	}
	prop := c.expr(m.prop)
	return func(f *frame) Value {
		ov := obj(f)
		if inChain {
			if ov.isShort() {
				return shortV
			}
			if optional && ov.isNullish() {
				return shortV
			}
		}
		kv := prop(f)
		if o, ok := ov.r.(*object); ok && o.class == classArray && kv.r == nil {
			i := int(kv.n)
			if float64(i) == kv.n && i >= 0 && i < len(o.arr) {
				if v := o.arr[i]; !o.holes || !v.isHole() {
					return v
				}
			}
		}
		f.pos = p
		return f.vm.getIndex(ov, kv)
	}
}

func (c *compiler) namedGet(obj expr, name string, p int32, inChain, optional bool) expr {
	key := strKey(name)
	ic := &getIC{}
	if name == "length" {
		return func(f *frame) Value {
			ov := obj(f)
			if inChain {
				if ov.isShort() || (optional && ov.isNullish()) {
					return shortV
				}
			}
			switch r := ov.r.(type) {
			case *object:
				if r.class == classArray {
					return Value{n: float64(len(r.arr))}
				}
			case *jstr:
				return Value{n: float64(r.length())}
			}
			f.pos = p
			return f.vm.getV(ov, key)
		}
	}
	return func(f *frame) Value {
		ov := obj(f)
		if inChain {
			if ov.isShort() || (optional && ov.isNullish()) {
				return shortV
			}
		}
		if o, ok := ov.r.(*object); ok {
			if o.shape == ic.shape {
				if ic.holder == nil {
					return o.slots[ic.idx]
				}
				if ic.holder.shape == ic.hshape {
					return ic.holder.slots[ic.idx]
				}
			}
			f.pos = p
			return f.vm.getCached(o, key, ic, ov)
		}
		f.pos = p
		return f.vm.getV(ov, key)
	}
}

func (c *compiler) args(list []node) (func(f *frame) []Value, bool) {
	spread := false
	exprs := make([]expr, len(list))
	isSpread := make([]bool, len(list))
	for i, a := range list {
		if s, ok := a.(*spreadNode); ok {
			spread = true
			isSpread[i] = true
			exprs[i] = c.expr(s.arg)
			continue
		}
		exprs[i] = c.expr(a)
	}
	if !spread {
		n := len(exprs)
		return func(f *frame) []Value {
			argv := f.vm.alloc(n)
			for i, e := range exprs {
				argv[i] = e(f)
			}
			return argv
		}, false
	}
	return func(f *frame) []Value {
		var argv []Value
		vm := f.vm
		for i, e := range exprs {
			v := e(f)
			if isSpread[i] {
				if o, ok := v.r.(*object); ok && o.class == classArray {
					argv = vm.appendV(argv, dense(o)...)
					continue
				}
				vm.iterate(v, func(x Value) bool {
					argv = vm.appendV(argv, x)
					return true
				})
				continue
			}
			argv = append(argv, v)
		}
		return argv
	}, true
}

func calleeName(n node) string {
	switch t := n.(type) {
	case *identNode:
		return t.name
	case *memberNode:
		base := calleeName(t.obj)
		if t.prop != nil {
			return base + "[...]"
		}
		if base == "" {
			return t.name
		}
		return base + "." + t.name
	case *thisNode:
		return "this"
	case *callNode:
		return calleeName(t.callee) + "(...)"
	}
	return "expression"
}

func (c *compiler) call(t *callNode) expr {
	inChain := c.chain > 0
	optional := t.optional
	argsF, _ := c.args(t.args)
	p := int32(t.pos)
	name := calleeName(t.callee)
	if m, ok := t.callee.(*memberNode); ok {
		obj := c.expr(m.obj)
		var getter func(f *frame, ov Value) Value
		if m.prop == nil {
			key := strKey(m.name)
			ic := &getIC{}
			getter = func(f *frame, ov Value) Value {
				if o, ok := ov.r.(*object); ok {
					if o.shape == ic.shape {
						if ic.holder == nil {
							return o.slots[ic.idx]
						}
						if ic.holder.shape == ic.hshape {
							return ic.holder.slots[ic.idx]
						}
					}
					return f.vm.getCached(o, key, ic, ov)
				}
				return f.vm.getV(ov, key)
			}
		} else {
			prop := c.expr(m.prop)
			getter = func(f *frame, ov Value) Value { return f.vm.getIndex(ov, prop(f)) }
		}
		mOptional := m.optional
		return func(f *frame) Value {
			ov := obj(f)
			if inChain {
				if ov.isShort() || (mOptional && ov.isNullish()) {
					return shortV
				}
			}
			f.pos = p
			fnV := getter(f, ov)
			if optional && fnV.isNullish() {
				return shortV
			}
			vm := f.vm
			sp := vm.sp
			argv := argsF(f)
			fo, ok := fnV.r.(*object)
			if !ok || fo.class != classFunction {
				f.pos = p
				vm.throwTypeError(name + " is not a function")
			}
			f.pos = p
			r := vm.callObj(fo, ov, argv)
			vm.sp = sp
			return r
		}
	}
	callee := c.expr(t.callee)
	return func(f *frame) Value {
		fnV := callee(f)
		if inChain {
			if fnV.isShort() || (optional && fnV.isNullish()) {
				return shortV
			}
		}
		vm := f.vm
		sp := vm.sp
		argv := argsF(f)
		fo, ok := fnV.r.(*object)
		f.pos = p
		if !ok || fo.class != classFunction {
			vm.throwTypeError(name + " is not a function")
		}
		r := vm.callObj(fo, undefined, argv)
		vm.sp = sp
		return r
	}
}

func (c *compiler) newExpr(t *newNode) expr {
	callee := c.expr(t.callee)
	argsF, _ := c.args(t.args)
	p := int32(t.pos)
	name := calleeName(t.callee)
	return func(f *frame) Value {
		fnV := callee(f)
		vm := f.vm
		sp := vm.sp
		argv := argsF(f)
		f.pos = p
		fo, ok := fnV.r.(*object)
		if !ok || fo.class != classFunction {
			vm.throwTypeError(name + " is not a constructor")
		}
		r := vm.construct(fnV, argv, nil)
		vm.sp = sp
		return r
	}
}
