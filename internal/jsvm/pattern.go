package jsvm

func (c *compiler) patternDecl(n node, kind string) func(f *frame, v Value) {
	return c.patternWith(n, func(id *identNode) func(f *frame, v Value) {
		if kind == "var" {
			return c.store(id.ref, id.pos)
		}
		return c.initStore(id.ref, id.pos, kind == "const")
	})
}

func (c *compiler) pattern(n node, decl bool) func(f *frame, v Value) {
	return c.patternWith(n, func(id *identNode) func(f *frame, v Value) {
		if decl {
			return c.initStore(id.ref, id.pos, false)
		}
		return c.store(id.ref, id.pos)
	})
}

type identStore func(id *identNode) func(f *frame, v Value)

func (c *compiler) patternWith(n node, ids identStore) func(f *frame, v Value) {
	switch t := n.(type) {
	case *identNode:
		return ids(t)
	case *memberNode:
		obj := c.expr(t.obj)
		key := c.memberKey(t)
		p := int32(t.pos)
		return func(f *frame, v Value) {
			ov := obj(f)
			k := key(f)
			f.pos = p
			f.vm.setV(ov, k, v)
		}
	case *defaultNode:
		target := c.patternWith(t.target, ids)
		def := c.namedExpr(t.value, t.target)
		return func(f *frame, v Value) {
			if v.isUndef() {
				v = def(f)
			}
			target(f, v)
		}
	case *arrayPattern:
		return c.arrayPatternWith(t, ids)
	case *objectPattern:
		return c.objectPatternWith(t, ids)
	}
	c.fail(n.at(), "Invalid destructuring assignment target")
	return nil
}

func (c *compiler) arrayPatternWith(t *arrayPattern, ids identStore) func(f *frame, v Value) {
	type el struct {
		rest   bool
		assign func(f *frame, v Value)
	}
	elems := make([]el, len(t.elems))
	for i, e := range t.elems {
		switch x := e.(type) {
		case nil:
		case *restNode:
			elems[i] = el{true, c.patternWith(x.arg, ids)}
		default:
			elems[i] = el{false, c.patternWith(x, ids)}
		}
	}
	p := int32(t.pos)
	return func(f *frame, v Value) {
		vm := f.vm
		var vals []Value
		if o, ok := v.r.(*object); ok && o.class == classArray {
			vals = dense(o)
		} else {
			f.pos = p
			vals = vm.collect(v)
		}
		for i, e := range elems {
			if e.assign == nil {
				continue
			}
			if e.rest {
				var rest []Value
				if i < len(vals) {
					rest = append([]Value(nil), vals[i:]...)
				} else {
					rest = []Value{}
				}
				e.assign(f, vm.newArrayV(rest))
				continue
			}
			x := undefined
			if i < len(vals) {
				x = vals[i]
			}
			e.assign(f, x)
		}
	}
}

func (c *compiler) objectPatternWith(t *objectPattern, ids identStore) func(f *frame, v Value) {
	type prop struct {
		key    propKey
		kexpr  expr
		assign func(f *frame, v Value)
	}
	props := make([]prop, len(t.props))
	for i, pr := range t.props {
		if pr.computed {
			props[i] = prop{kexpr: c.expr(pr.key), assign: c.patternWith(pr.value, ids)}
			continue
		}
		props[i] = prop{key: strKey(c.propKeyName(pr)), assign: c.patternWith(pr.value, ids)}
	}
	var rest func(f *frame, v Value)
	if t.rest != nil {
		rest = c.patternWith(t.rest, ids)
	}
	p := int32(t.pos)
	return func(f *frame, v Value) {
		vm := f.vm
		if v.isNullish() {
			f.pos = p
			name := ""
			if len(props) > 0 && props[0].kexpr == nil {
				name = props[0].key.str()
			}
			if name != "" {
				vm.throwTypeError("Cannot destructure property '" + name + "' of '" + ToString(v) + "' as it is " + ToString(v) + ".")
			}
			vm.throwTypeError("Cannot destructure '" + ToString(v) + "' as it is " + ToString(v) + ".")
		}
		var used []propKey
		for _, pr := range props {
			k := pr.key
			if pr.kexpr != nil {
				k = vm.toPropertyKey(pr.kexpr(f))
			}
			if rest != nil {
				used = append(used, k)
			}
			f.pos = p
			pr.assign(f, vm.getV(v, k))
		}
		if rest != nil {
			o := vm.newObject()
			vm.copyDataProps(o, v, used)
			rest(f, objV(o))
		}
	}
}
