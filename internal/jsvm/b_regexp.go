package jsvm

import (
	"fmt"

	"github.com/paul-nameless/qi/internal/jsvm/regex"
	"strconv"
	"strings"
)

type reEngine interface {
	ExecSubject(sub *regex.Subject, startUTF16 int) []int
	ExecSubjectBytes(sub *regex.Subject, startByte int) []int
	NumGroups() int
	GroupNames() []string
	Source() string
	Flags() string
	Global() bool
	Sticky() bool
	Unicode() bool
	IgnoreCase() bool
	Multiline() bool
	DotAll() bool
	HasIndices() bool
}

type jsRegex struct {
	eng    reEngine
	names  []string
	named  bool
	global bool
	sticky bool
}

type regexpData struct {
	re *jsRegex
}

func compileRegex(pattern, flags string) (*jsRegex, error) {
	eng, err := compileEngine(pattern, flags)
	if err != nil {
		return nil, err
	}
	r := &jsRegex{eng: eng, names: eng.GroupNames(), global: eng.Global(), sticky: eng.Sticky()}
	for _, n := range r.names {
		if n != "" {
			r.named = true
		}
	}
	return r, nil
}

func (vm *VM) execUnits(re *jsRegex, s *jstr, start int) (m []int) {
	defer func() {
		if r := recover(); r != nil {
			switch r.(type) {
			case *interruptSignal, *jsThrow, killSignal:
				panic(r)
			}
			msg := fmt.Sprint(r)
			if e, ok := r.(error); ok {
				msg = e.Error()
			}
			if strings.Contains(msg, "too complex") {
				vm.throwError("Error", msg)
			}
			vm.throwError("Error", "regular expression failed: "+msg)
		}
	}()
	if s.isASCII() {
		vm.flat(s)
		return re.eng.ExecSubjectBytes(s.regexSubject(), start)
	}
	vm.flat(s)
	s.scan()
	if s.info&strAstral == 0 {
		m = re.eng.ExecSubjectBytes(s.regexSubject(), s.byteOf(start))
		for i, b := range m {
			if b >= 0 {
				m[i] = s.unitAt(b)
			}
		}
		return m
	}
	return re.eng.ExecSubject(s.regexSubject(), start)
}

func (vm *VM) newRegExpObj(re *jsRegex) *object {
	o := &object{shape: vm.regexpRoot, class: classRegExp, inner: &regexpData{re: re}, slots: []Value{num(0)}}
	return o
}

func (vm *VM) regexpProp(o *object, k string) (Value, bool) {
	rd, ok := o.inner.(*regexpData)
	if !ok {
		return undefined, false
	}
	e := rd.re.eng
	switch k {
	case "source":
		return strV(e.Source()), true
	case "flags":
		return strV(e.Flags()), true
	case "global":
		return boolV(e.Global()), true
	case "ignoreCase":
		return boolV(e.IgnoreCase()), true
	case "multiline":
		return boolV(e.Multiline()), true
	case "sticky":
		return boolV(e.Sticky()), true
	case "unicode":
		return boolV(e.Unicode()), true
	case "dotAll":
		return boolV(e.DotAll()), true
	case "hasIndices":
		return boolV(e.HasIndices()), true
	}
	return undefined, false
}

func (vm *VM) thisRegExp(this Value, name string) (*object, *jsRegex) {
	o, ok := this.r.(*object)
	if ok && o.class == classRegExp {
		if rd, ok := o.inner.(*regexpData); ok {
			return o, rd.re
		}
	}
	vm.throwTypeError("RegExp.prototype." + name + " called on incompatible receiver")
	return nil, nil
}

func (vm *VM) setupRegExp() {
	p := vm.newObject()
	vm.regexpProto = p
	vm.regexpRoot = p.rootShape().withProp(strKey("lastIndex"), fWritable)
	build := func(vm *VM, args []Value, nt *object) Value {
		pat, fl := arg(args, 0), arg(args, 1)
		pattern, flags := "", ""
		if o, ok := pat.r.(*object); ok && o.class == classRegExp {
			re := o.inner.(*regexpData).re
			pattern, flags = re.eng.Source(), re.eng.Flags()
		} else if !pat.isUndef() {
			pattern = vm.toString(pat)
		}
		if !fl.isUndef() {
			flags = vm.toString(fl)
		}
		re, err := compileRegex(pattern, flags)
		if err != nil {
			vm.throwError("SyntaxError", err.Error())
		}
		return objV(vm.newRegExpObj(re))
	}
	vm.newCtor("RegExp", 2, p, func(vm *VM, this Value, args []Value) Value {
		return build(vm, args, nil)
	}, build)
	vm.method(p, "exec", 1, func(vm *VM, this Value, args []Value) Value {
		o, re := vm.thisRegExp(this, "exec")
		return vm.regexExec(o, re, vm.toStr(arg(args, 0)))
	})
	vm.method(p, "test", 1, func(vm *VM, this Value, args []Value) Value {
		o, re := vm.thisRegExp(this, "test")
		s := vm.toStr(arg(args, 0))
		if !re.global && !re.sticky {
			return boolV(vm.execUnits(re, s, 0) != nil)
		}
		return boolV(!vm.regexExec(o, re, s).isNull())
	})
	vm.method(p, "toString", 0, func(vm *VM, this Value, args []Value) Value {
		_, re := vm.thisRegExp(this, "toString")
		return strV("/" + re.eng.Source() + "/" + re.eng.Flags())
	})
}

func (vm *VM) lastIndex(o *object) int {
	return vm.toLength(vm.getProp(o, strKey("lastIndex"), objV(o)))
}

func (vm *VM) setLastIndex(o *object, i int) {
	vm.setProp(o, strKey("lastIndex"), intV(i))
}

func (vm *VM) regexExec(o *object, re *jsRegex, s *jstr) Value {
	start := 0
	useLast := re.global || re.sticky
	if useLast {
		li := vm.lastIndex(o)
		if li > s.length() {
			vm.setLastIndex(o, 0)
			return null
		}
		start = li
	}
	m := vm.execUnits(re, s, start)
	if m == nil {
		if useLast {
			vm.setLastIndex(o, 0)
		}
		return null
	}
	res := vm.matchArray(re, s, m)
	if useLast {
		vm.setLastIndex(o, m[1])
	}
	return res
}

func (vm *VM) group(s *jstr, m []int, i int) Value {
	if 2*i+1 >= len(m) || m[2*i] < 0 {
		return undefined
	}
	return vm.substr(s, m[2*i], m[2*i+1])
}

func (vm *VM) groupsObject(re *jsRegex, vals []Value) Value {
	if !re.named {
		return undefined
	}
	g := vm.newObjectProto(nil)
	for i, name := range re.names {
		if name != "" && i < len(vals) {
			g.setOwnStr(name, vals[i])
		}
	}
	return objV(g)
}

func (vm *VM) matchArray(re *jsRegex, s *jstr, m []int) Value {
	n := len(m) / 2
	vals := make([]Value, n)
	for i := range vals {
		vals[i] = vm.group(s, m, i)
	}
	a := vm.newArray(vals)
	a.addProp(strKey("index"), intV(m[0]), fDefault)
	a.addProp(strKey("input"), strVal(s), fDefault)
	a.addProp(strKey("groups"), vm.groupsObject(re, vals), fDefault)
	if re.eng.HasIndices() {
		ind := make([]Value, n)
		for i := range n {
			if m[2*i] < 0 {
				ind[i] = undefined
			} else {
				ind[i] = vm.newArrayV([]Value{intV(m[2*i]), intV(m[2*i+1])})
			}
		}
		a.addProp(strKey("indices"), vm.newArrayV(ind), fDefault)
	}
	return objV(a)
}

func advance(s *jstr, re *jsRegex, i int) int {
	if re.eng.Unicode() && !s.isASCII() && i+1 < s.length() {
		c := s.charCodeAt(i)
		if c >= 0xD800 && c < 0xDC00 {
			d := s.charCodeAt(i + 1)
			if d >= 0xDC00 && d < 0xE000 {
				return i + 2
			}
		}
	}
	return i + 1
}

func (vm *VM) allMatches(re *jsRegex, s *jstr, fn func(m []int) bool) {
	pos, n := 0, s.length()
	for pos <= n {
		m := vm.execUnits(re, s, pos)
		if m == nil || !fn(m) {
			return
		}
		if m[1] == m[0] {
			pos = advance(s, re, m[1])
		} else {
			pos = m[1]
		}
		if vm.intr.Load() != 0 {
			panic(&interruptSignal{})
		}
	}
}

func asRegExp(v Value) (*object, *jsRegex) {
	if o, ok := v.r.(*object); ok && o.class == classRegExp {
		if rd, ok := o.inner.(*regexpData); ok {
			return o, rd.re
		}
	}
	return nil, nil
}

func (vm *VM) coerceRegExp(pat Value, flags string) *jsRegex {
	src := ""
	if !pat.isUndef() {
		src = vm.toString(pat)
	}
	re, err := compileRegex(src, flags)
	if err != nil {
		vm.throwError("SyntaxError", err.Error())
	}
	return re
}

func (vm *VM) strMatch(s *jstr, pat Value) Value {
	o, re := asRegExp(pat)
	if re == nil {
		re = vm.coerceRegExp(pat, "")
		o = vm.newRegExpObj(re)
	}
	if !re.global {
		return vm.regexExec(o, re, s)
	}
	var out []Value
	vm.allMatches(re, s, func(m []int) bool {
		out = vm.appendV(out, vm.substr(s, m[0], m[1]))
		vm.track(64)
		return true
	})
	vm.setLastIndex(o, 0)
	if out == nil {
		return null
	}
	return vm.newArrayV(out)
}

func (vm *VM) strMatchAll(s *jstr, pat Value) Value {
	o, re := asRegExp(pat)
	pos := 0
	if re == nil {
		re = vm.coerceRegExp(pat, "g")
	} else if !re.global {
		vm.throwTypeError("String.prototype.matchAll called with a non-global RegExp argument")
	} else {
		pos = vm.lastIndex(o)
	}
	n := s.length()
	return vm.newIter(&iterState{fn: func() (Value, bool) {
		if pos > n {
			return undefined, false
		}
		m := vm.execUnits(re, s, pos)
		if m == nil {
			pos = n + 1
			return undefined, false
		}
		if m[1] == m[0] {
			pos = advance(s, re, m[1])
		} else {
			pos = m[1]
		}
		return vm.matchArray(re, s, m), true
	}})
}

func (vm *VM) strSearch(s *jstr, pat Value) Value {
	_, re := asRegExp(pat)
	if re == nil {
		re = vm.coerceRegExp(pat, "")
	}
	m := vm.execUnits(re, s, 0)
	if m == nil {
		return intV(-1)
	}
	return intV(m[0])
}

func (vm *VM) strReplace(s *jstr, pat, repl Value, all bool) Value {
	fn, isFn := repl.r.(*object)
	isFn = isFn && fn.isCallable()
	var replStr string
	if !isFn {
		replStr = vm.toString(repl)
	}
	if o, re := asRegExp(pat); re != nil {
		return vm.regexReplace(s, o, re, fn, isFn, replStr, all)
	}
	str := s.flat()
	b := sbuf{vm: vm}
	last := 0
	needle := vm.toString(pat)
	pos := strings.Index(str, needle)
	if pos < 0 {
		return strVal(s)
	}
	sub := func(a, z int) string { return str[a:z] }
	for pos >= 0 {
		m := []int{pos, pos + len(needle)}
		b.add(str[last:pos])
		if isFn {
			r := vm.call3(fn, undefined, strV(needle), intV(s.unitAt(pos)), strVal(s))
			b.add(vm.toString(r))
		} else {
			expandReplacement(&b, replStr, sub, len(str), m, nil)
		}
		last = m[1]
		if !all {
			break
		}
		next := m[1]
		if len(needle) == 0 {
			if next >= len(str) {
				break
			}
			_, size := decodeRune(str[next:])
			b.add(str[next : next+size])
			next += size
			last = next
		}
		i := strings.Index(str[next:], needle)
		if i < 0 {
			break
		}
		pos = next + i
	}
	b.add(str[last:])
	vm.track(b.Len())
	return strV(b.String())
}

func (vm *VM) regexReplace(s *jstr, o *object, re *jsRegex, fn *object, isFn bool, replStr string, all bool) Value {
	if all && !re.global {
		vm.throwTypeError("replaceAll must be called with a global RegExp")
	}
	n := s.length()
	sub := func(a, z int) string { return s.substring(a, z) }
	b := sbuf{vm: vm}
	last, changed := 0, false
	apply := func(m []int) {
		changed = true
		b.add(sub(last, m[0]))
		if isFn {
			b.add(vm.callReplacer(fn, re, s, m))
		} else {
			expandReplacement(&b, replStr, sub, n, m, re)
		}
		last = m[1]
	}
	if re.global {
		vm.allMatches(re, s, func(m []int) bool {
			apply(m)
			return true
		})
		vm.setLastIndex(o, 0)
	} else {
		start := 0
		if re.sticky {
			start = vm.lastIndex(o)
		}
		if m := vm.execUnits(re, s, start); m != nil {
			apply(m)
			if re.sticky {
				vm.setLastIndex(o, m[1])
			}
		} else if re.sticky {
			vm.setLastIndex(o, 0)
		}
	}
	if !changed {
		return strVal(s)
	}
	b.add(sub(last, n))
	vm.track(b.Len())
	return strV(b.String())
}

func (vm *VM) callReplacer(fn *object, re *jsRegex, s *jstr, m []int) string {
	n := len(m) / 2
	args := make([]Value, 0, n+3)
	for i := range n {
		args = append(args, vm.group(s, m, i))
	}
	args = append(args, intV(m[0]), strVal(s))
	if re.named {
		args = append(args, vm.groupsObject(re, args[:n]))
	}
	return vm.toString(vm.callObj(fn, undefined, args))
}

func expandReplacement(b *sbuf, repl string, sub func(a, z int) string, total int, m []int, re *jsRegex) {
	if strings.IndexByte(repl, '$') < 0 {
		b.add(repl)
		return
	}
	n := len(m) / 2
	group := func(i int) {
		if i < n && m[2*i] >= 0 {
			b.add(sub(m[2*i], m[2*i+1]))
		}
	}
	for i := 0; i < len(repl); i++ {
		c := repl[i]
		if c != '$' || i+1 >= len(repl) {
			b.addByte(c)
			continue
		}
		d := repl[i+1]
		switch {
		case d == '$':
			b.addByte('$')
			i++
		case d == '&':
			group(0)
			i++
		case d == '`':
			b.add(sub(0, m[0]))
			i++
		case d == '\'':
			b.add(sub(m[1], total))
			i++
		case d >= '0' && d <= '9':
			idx := int(d - '0')
			used := 1
			if i+2 < len(repl) && repl[i+2] >= '0' && repl[i+2] <= '9' {
				two := idx*10 + int(repl[i+2]-'0')
				if two >= 1 && two < n {
					idx, used = two, 2
				}
			}
			if idx >= 1 && idx < n {
				group(idx)
				i += used
			} else {
				b.addByte('$')
			}
		case d == '<' && re != nil && re.named:
			end := strings.IndexByte(repl[i+2:], '>')
			if end < 0 {
				b.addByte('$')
				continue
			}
			name := repl[i+2 : i+2+end]
			for gi, gn := range re.names {
				if gn == name {
					group(gi)
				}
			}
			i += end + 2
		default:
			b.addByte('$')
		}
	}
}

func (vm *VM) strSplit(s *jstr, sepV, limV Value) Value {
	lim := -1
	if !limV.isUndef() {
		lim = int(toUint32(vm.toNumber(limV)))
	}
	str := s.flat()
	out := []Value{}
	if lim == 0 {
		return vm.newArrayV(out)
	}
	if _, re := asRegExp(sepV); re != nil {
		return vm.regexSplit(s, re, lim)
	}
	if sepV.isUndef() {
		return vm.newArrayV([]Value{strVal(s)})
	}
	sep := vm.toString(sepV)
	if sep == "" {
		pieces := len(str)
		if lim >= 0 && lim < pieces {
			pieces = lim
		}
		vm.reserveValues(pieces)
		vm.track(24 * pieces)
		if s.isASCII() {
			for i := 0; i < len(str) && (lim < 0 || len(out) < lim); i++ {
				out = append(out, Value{r: asciiChars[str[i]]})
			}
			return vm.newArrayV(out)
		}
		u := s.units()
		for i := range u {
			if lim >= 0 && len(out) >= lim {
				break
			}
			out = append(out, strV(unitsToString(u[i:i+1])))
		}
		return vm.newArrayV(out)
	}
	pieces := strings.Count(str, sep) + 1
	if lim >= 0 && lim < pieces {
		pieces = lim
	}
	vm.reserveValues(pieces)
	out = make([]Value, 0, pieces)
	for {
		i := strings.Index(str, sep)
		if i < 0 || (lim >= 0 && len(out) >= lim) {
			break
		}
		out = vm.appendV(out, strV(str[:i]))
		vm.track(64)
		str = str[i+len(sep):]
	}
	if lim < 0 || len(out) < lim {
		out = append(out, strV(str))
	}
	return vm.newArrayV(out)
}

func (vm *VM) regexSplit(s *jstr, re *jsRegex, lim int) Value {
	if re.sticky {
		flags := strings.ReplaceAll(re.eng.Flags(), "y", "")
		plain, err := compileRegex(re.eng.Source(), flags)
		if err != nil {
			vm.throwError("SyntaxError", err.Error())
		}
		re = plain
	}
	n := s.length()
	out := []Value{}
	if n == 0 {
		if vm.execUnits(re, s, 0) != nil {
			return vm.newArrayV(out)
		}
		return vm.newArrayV([]Value{strVal(s)})
	}
	last, pos := 0, 0
	for pos < n {
		m := vm.execUnits(re, s, pos)
		if m == nil || m[0] >= n {
			break
		}
		if m[1] == last {
			pos = advance(s, re, m[0])
			continue
		}
		out = vm.appendV(out, vm.substr(s, last, m[0]))
		vm.track(64)
		if vm.intr.Load() != 0 {
			panic(&interruptSignal{})
		}
		if lim > 0 && len(out) >= lim {
			return vm.newArrayV(out)
		}
		for i := 1; i < len(m)/2; i++ {
			out = vm.appendV(out, vm.group(s, m, i))
			vm.track(64)
			if lim > 0 && len(out) >= lim {
				return vm.newArrayV(out)
			}
		}
		last = m[1]
		pos = last
	}
	out = append(out, vm.substr(s, last, n))
	return vm.newArrayV(out)
}

func itoa(i int) string { return strconv.Itoa(i) }
