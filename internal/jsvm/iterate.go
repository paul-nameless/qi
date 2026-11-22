package jsvm

import "unicode/utf8"

type mapEntry struct {
	key, val   Value
	next, prev *mapEntry
}

func (e *mapEntry) deleted() bool { return e.prev == e }

type mapData struct {
	strs       map[string]*mapEntry
	nums       map[float64]*mapEntry
	others     map[any]*mapEntry
	head, tail *mapEntry
	size       int
	dangling   []*mapEntry
}

type nanKey struct{}

func newMapData() *mapData { return &mapData{} }

func numKey(f float64) float64 {
	if f == 0 {
		return 0
	}
	return f
}

func (m *mapData) get(k Value) (*mapEntry, bool) {
	switch r := k.r.(type) {
	case nil:
		if k.n != k.n {
			e, ok := m.others[nanKey{}]
			return e, ok
		}
		e, ok := m.nums[numKey(k.n)]
		return e, ok
	case *jstr:
		e, ok := m.strs[r.flat()]
		return e, ok
	case boolT:
		e, ok := m.others[k.n != 0]
		return e, ok
	}
	e, ok := m.others[k.r]
	return e, ok
}

func (m *mapData) put(k Value, e *mapEntry) {
	switch r := k.r.(type) {
	case nil:
		if k.n != k.n {
			m.otherPut(nanKey{}, e)
			return
		}
		if m.nums == nil {
			m.nums = map[float64]*mapEntry{}
		}
		m.nums[numKey(k.n)] = e
	case *jstr:
		if m.strs == nil {
			m.strs = map[string]*mapEntry{}
		}
		m.strs[r.flat()] = e
	case boolT:
		m.otherPut(k.n != 0, e)
	default:
		m.otherPut(k.r, e)
	}
}

func (m *mapData) otherPut(k any, e *mapEntry) {
	if m.others == nil {
		m.others = map[any]*mapEntry{}
	}
	m.others[k] = e
}

func (m *mapData) drop(k Value) {
	switch r := k.r.(type) {
	case nil:
		if k.n != k.n {
			delete(m.others, nanKey{})
			return
		}
		delete(m.nums, numKey(k.n))
	case *jstr:
		delete(m.strs, r.flat())
	case boolT:
		delete(m.others, k.n != 0)
	default:
		delete(m.others, k.r)
	}
}

func (m *mapData) set(k, v Value) {
	if e, ok := m.get(k); ok {
		e.val = v
		return
	}
	if k.r == nil && k.n == 0 {
		k = num(0)
	}
	e := &mapEntry{key: k, val: v, prev: m.tail}
	for _, d := range m.dangling {
		d.next = e
	}
	m.dangling = m.dangling[:0]
	if m.tail != nil {
		m.tail.next = e
	} else {
		m.head = e
	}
	m.tail = e
	m.put(k, e)
	m.size++
}

func (m *mapData) remove(k Value) bool {
	e, ok := m.get(k)
	if !ok {
		return false
	}
	m.drop(k)
	if e.prev != nil {
		e.prev.next = e.next
	} else {
		m.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else {
		m.tail = e.prev
		m.dangling = append(m.dangling, e)
	}
	e.prev = e
	m.size--
	return true
}

func (m *mapData) clear() {
	for e := m.head; e != nil; {
		next := e.next
		e.prev = e
		e = next
	}
	if m.tail != nil {
		m.dangling = append(m.dangling, m.tail)
	}
	m.strs, m.nums, m.others = nil, nil, nil
	m.head, m.tail, m.size = nil, nil, 0
}

func (m *mapData) each(fn func(e *mapEntry) bool) {
	e := m.head
	for e != nil {
		if !e.deleted() {
			if !fn(e) {
				return
			}
		}
		e = e.next
	}
}

const (
	iterKeys uint8 = iota
	iterValues
	iterEntries
)

type iterState struct {
	mode  uint8
	arr   *object
	i     int
	md    *mapData
	cur   *mapEntry
	start bool
	s     string
	isSet bool
	done  bool
	src   Value
	fn    func() (Value, bool)
}

func (vm *VM) newIter(st *iterState) Value {
	return objV(&object{shape: vm.iterProto.rootShape(), class: classIter, inner: st})
}

func (vm *VM) iterNext(st *iterState) (Value, bool) {
	if st.done {
		return undefined, false
	}
	switch {
	case st.fn != nil:
		v, ok := st.fn()
		if !ok {
			st.done = true
		}
		return v, ok
	case st.arr != nil:
		if st.i >= len(st.arr.arr) {
			st.done = true
			return undefined, false
		}
		i := st.i
		st.i++
		switch st.mode {
		case iterKeys:
			return intV(i), true
		case iterEntries:
			return vm.newArrayV([]Value{intV(i), unhole(st.arr.arr[i])}), true
		}
		return unhole(st.arr.arr[i]), true
	case st.md != nil:
		var e *mapEntry
		if !st.start {
			st.start = true
			e = st.md.head
		} else if st.cur != nil {
			e = st.cur.next
		}
		for e != nil && e.deleted() {
			e = e.next
		}
		if e == nil {
			st.done = true
			return undefined, false
		}
		st.cur = e
		switch {
		case st.mode == iterEntries && st.isSet:
			return vm.newArrayV([]Value{e.key, e.key}), true
		case st.mode == iterEntries:
			return vm.newArrayV([]Value{e.key, e.val}), true
		case st.mode == iterValues && !st.isSet:
			return e.val, true
		}
		return e.key, true
	default:
		if st.i >= len(st.s) {
			st.done = true
			return undefined, false
		}
		_, size := utf8.DecodeRuneInString(st.s[st.i:])
		v := strV(st.s[st.i : st.i+size])
		st.i += size
		return v, true
	}
}

func (vm *VM) iterate(v Value, fn func(Value) bool) {
	switch r := v.r.(type) {
	case *object:
		switch r.class {
		case classArray:
			for i := 0; i < len(r.arr); i++ {
				if !fn(unhole(r.arr[i])) {
					return
				}
			}
			return
		case classMap:
			md := r.inner.(*mapData)
			md.each(func(e *mapEntry) bool { return fn(vm.newArrayV([]Value{e.key, e.val})) })
			return
		case classSet:
			md := r.inner.(*mapData)
			md.each(func(e *mapEntry) bool { return fn(e.key) })
			return
		case classIter:
			st := r.inner.(*iterState)
			for {
				x, ok := vm.iterNext(st)
				if !ok || !fn(x) {
					return
				}
			}
		case classString:
			vm.iterate(strVal(r.inner.(*jstr)), fn)
			return
		}
	case *jstr:
		s := r.flat()
		for i := 0; i < len(s); {
			c := s[i]
			if c < 0x80 {
				if !fn(Value{r: asciiChars[c]}) {
					return
				}
				i++
				continue
			}
			_, size := utf8.DecodeRuneInString(s[i:])
			if !fn(strV(s[i : i+size])) {
				return
			}
			i += size
		}
		return
	}
	vm.throwTypeError(vm.iterName(v) + " is not iterable")
}

func (vm *VM) iterName(v Value) string {
	switch v.r.(type) {
	case *object:
		return "object"
	case undefT, nullT:
		return ToString(v)
	case nil:
		return "number " + ToString(v)
	}
	return typeOf(v) + " " + ToString(v)
}

func dense(o *object) []Value {
	if !o.holes {
		return o.arr
	}
	out := make([]Value, len(o.arr))
	for i, v := range o.arr {
		out[i] = unhole(v)
	}
	return out
}

func (vm *VM) collect(v Value) []Value {
	if o, ok := v.r.(*object); ok && o.class == classArray {
		return dense(o)
	}
	var out []Value
	vm.iterate(v, func(x Value) bool {
		out = vm.appendV(out, x)
		return true
	})
	return out
}

func (vm *VM) isIterable(v Value) bool {
	switch r := v.r.(type) {
	case *jstr:
		return true
	case *object:
		switch r.class {
		case classArray, classMap, classSet, classIter, classString:
			return true
		}
	}
	return false
}
