package jsvm

import (
	"fmt"
	"hash/maphash"
	"math"
	"sort"
	"strconv"
	"strings"
)

type objClass uint8

const (
	classObject objClass = iota
	classArray
	classFunction
	classError
	classMap
	classSet
	classPromise
	classRegExp
	classGlobal
	classIter
	classBoolean
	classNumber
	classString
	classDate
)

type propFlag uint8

const (
	fWritable propFlag = 1 << iota
	fEnumerable
	fConfigurable
	fAccessor
	fDeleted
)

const fDefault = fWritable | fEnumerable | fConfigurable
const fHidden = fWritable | fConfigurable

type propKey string

func strKey(s string) propKey { return propKey(s) }

func symKey(sym *symbol) propKey { return propKey(fmt.Sprintf("\xffsym%p", sym)) }

func (k propKey) isSym() bool { return len(k) > 0 && k[0] == 0xff }

func (k propKey) str() string { return string(k) }

func (k propKey) String() string {
	if k.isSym() {
		return "Symbol()"
	}
	return string(k)
}

type shapeProp struct {
	key   propKey
	flags propFlag
}

type transKey struct {
	key   propKey
	flags propFlag
}

type hclass struct {
	proto   *object
	props   []shapeProp
	table   []int32
	trans   map[transKey]*hclass
	lastKey transKey
	last    *hclass
	dict    bool
	dead    int
	json    *shapeJSON
}

const indexMin = 8
const dictMax = 64

func newRootShape(proto *object) *hclass { return &hclass{proto: proto} }

var hashSeed = maphash.MakeSeed()

func buildTable(props []shapeProp) []int32 {
	size := 16
	for size < 2*len(props)+2 {
		size *= 2
	}
	t := make([]int32, size)
	for i := range t {
		t[i] = -1
	}
	mask := uint64(size - 1)
	for i, p := range props {
		if p.flags&fDeleted != 0 {
			continue
		}
		h := maphash.String(hashSeed, string(p.key)) & mask
		for t[h] >= 0 {
			h = (h + 1) & mask
		}
		t[h] = int32(i)
	}
	return t
}

func (s *hclass) find(k propKey) int {
	t := s.table
	mask := uint64(len(t) - 1)
	h := maphash.String(hashSeed, string(k)) & mask
	for {
		e := t[h]
		if e < 0 {
			return -1
		}
		if p := &s.props[e]; p.key == k && p.flags&fDeleted == 0 {
			return int(e)
		}
		h = (h + 1) & mask
	}
}

func (s *hclass) tableAdd(i int) {
	if 2*(len(s.props)+1) > len(s.table) {
		s.table = buildTable(s.props)
		return
	}
	t := s.table
	mask := uint64(len(t) - 1)
	h := maphash.String(hashSeed, string(s.props[i].key)) & mask
	for t[h] >= 0 {
		h = (h + 1) & mask
	}
	t[h] = int32(i)
}

func (s *hclass) lookup(k propKey) int {
	if s.table != nil {
		return s.find(k)
	}
	for i := range s.props {
		if s.props[i].key == k {
			return i
		}
	}
	return -1
}

func (s *hclass) lookupStr(k string) int {
	if s.table != nil {
		return s.find(propKey(k))
	}
	for i := range s.props {
		p := &s.props[i]
		if p.key.str() == k && !p.key.isSym() {
			return i
		}
	}
	return -1
}

func (s *hclass) withProp(k propKey, flags propFlag) *hclass {
	tk := transKey{k, flags}
	if s.last != nil && s.lastKey == tk {
		return s.last
	}
	if c, ok := s.trans[tk]; ok {
		s.lastKey, s.last = tk, c
		return c
	}
	n := len(s.props)
	props := make([]shapeProp, n+1)
	copy(props, s.props)
	k = propKey(strings.Clone(string(k)))
	tk.key = k
	props[n] = shapeProp{k, flags}
	c := &hclass{proto: s.proto, props: props}
	if n+1 > indexMin {
		c.table = buildTable(props)
	}
	if s.trans == nil {
		s.trans = make(map[transKey]*hclass, 1)
	}
	s.trans[tk] = c
	s.lastKey, s.last = tk, c
	return c
}

func (s *hclass) toDict() *hclass {
	props := make([]shapeProp, len(s.props), len(s.props)+4)
	copy(props, s.props)
	return &hclass{proto: s.proto, props: props, dict: true, table: buildTable(props)}
}

type object struct {
	shape    *hclass
	slots    []Value
	arr      []Value
	inner    any
	class    objClass
	noExt    bool
	isFrozen bool
	holes    bool
}

func (o *object) proto() *object { return o.shape.proto }

func (o *object) rootShape() *hclass {
	if o.class != classObject {
		return newRootShape(o)
	}
	if r, ok := o.inner.(*hclass); ok {
		return r
	}
	r := newRootShape(o)
	o.inner = r
	return r
}

func (vm *VM) newObject() *object {
	return &object{shape: vm.objectRoot}
}

func (vm *VM) newObjectProto(proto *object) *object {
	vm.track(64)
	if proto == nil {
		return &object{shape: vm.nullRoot}
	}
	return &object{shape: proto.rootShape()}
}

func (vm *VM) newArray(vals []Value) *object {
	vm.track(len(vals) * 24)
	return &object{shape: vm.arrayRoot, arr: vals, class: classArray}
}

func (vm *VM) newArrayV(vals []Value) Value { return objV(vm.newArray(vals)) }

func (o *object) isCallable() bool {
	if o.class != classFunction {
		return false
	}
	return true
}

func (o *object) getOwnSlot(k propKey) (int, propFlag) {
	i := o.shape.lookup(k)
	if i < 0 {
		return -1, 0
	}
	return i, o.shape.props[i].flags
}

func (o *object) addProp(k propKey, v Value, flags propFlag) {
	s := o.shape
	if s.dict {
		s.props = append(s.props, shapeProp{k, flags})
		s.tableAdd(len(s.props) - 1)
		o.slots = append(o.slots, v)
		return
	}
	if len(s.props) >= dictMax {
		o.shape = s.toDict()
		o.addProp(k, v, flags)
		return
	}
	o.shape = s.withProp(k, flags)
	o.slots = append(o.slots, v)
}

func (o *object) setOwn(k propKey, v Value) {
	if i := o.shape.lookup(k); i >= 0 {
		o.slots[i] = v
		return
	}
	o.addProp(k, v, fDefault)
}

func (o *object) setOwnStr(k string, v Value) { o.setOwn(propKey(k), v) }

func (o *object) defineHidden(k string, v Value) {
	key := propKey(k)
	if i := o.shape.lookup(key); i >= 0 {
		o.slots[i] = v
		return
	}
	o.addProp(key, v, fHidden)
}

func (o *object) deleteOwn(k propKey) bool {
	i := o.shape.lookup(k)
	if i < 0 {
		return true
	}
	if o.shape.props[i].flags&fConfigurable == 0 {
		return false
	}
	if !o.shape.dict {
		o.shape = o.shape.toDict()
	}
	s := o.shape
	s.props[i] = shapeProp{flags: fDeleted}
	o.slots[i] = undefined
	s.dead++
	if s.dead > 16 && s.dead*2 > len(s.props) {
		o.compact()
	}
	return true
}

func (o *object) compact() {
	s := o.shape
	props := make([]shapeProp, 0, len(s.props)-s.dead)
	slots := make([]Value, 0, len(props))
	for i, p := range s.props {
		if p.flags&fDeleted == 0 {
			props = append(props, p)
			slots = append(slots, o.slots[i])
		}
	}
	s.props, s.dead = props, 0
	s.table = buildTable(props)
	o.slots = slots
}

func (o *object) setProto(p *object) {
	if o.shape.dict {
		o.shape.proto = p
		return
	}
	var ns *hclass
	if p == nil {
		ns = &hclass{}
	} else {
		ns = p.rootShape()
	}
	for _, sp := range o.shape.props {
		ns = ns.withProp(sp.key, sp.flags)
	}
	o.shape = ns
}

func arrayIndex(s string) (int, bool) {
	if len(s) == 0 || len(s) > 10 {
		return 0, false
	}
	if s[0] == '0' {
		return 0, len(s) == 1
	}
	var n int64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	if n >= 4294967295 || n > math.MaxInt {
		return 0, false
	}
	return int(n), true
}

func (o *object) ownKeys(enumOnly bool) []propKey {
	var keys []propKey
	if o.class == classArray {
		for i := range o.arr {
			if !o.holes || !o.arr[i].isHole() {
				keys = append(keys, propKey(strconv.Itoa(i)))
			}
		}
	}
	if o.class == classGlobal {
		g := o.inner.(*globalStore)
		for _, b := range g.order {
			if b.present && (!enumOnly || b.enum) {
				keys = append(keys, propKey(b.name))
			}
		}
	}
	if o.class == classString {
		n := o.inner.(*jstr).length()
		for i := 0; i < n; i++ {
			keys = append(keys, propKey(strconv.Itoa(i)))
		}
	}
	start := len(keys)
	nIdx := 0
	for _, p := range o.shape.props {
		if p.flags&fDeleted != 0 || p.key.isSym() {
			continue
		}
		if enumOnly && p.flags&fEnumerable == 0 {
			continue
		}
		if _, ok := arrayIndex(p.key.str()); ok {
			nIdx++
		}
		keys = append(keys, p.key)
	}
	if nIdx > 0 {
		orderIndexKeys(keys[start:])
	}
	return keys
}

func orderIndexKeys(keys []propKey) {
	sort.SliceStable(keys, func(i, j int) bool {
		a, aok := arrayIndex(keys[i].str())
		b, bok := arrayIndex(keys[j].str())
		if aok && bok {
			return a < b
		}
		return aok && !bok
	})
}

type gbind struct {
	v       Value
	name    string
	present bool
	konst   bool
	enum    bool
}

type globalStore struct {
	m     map[string]*gbind
	order []*gbind
}

func (g *globalStore) bind(name string) *gbind {
	if b, ok := g.m[name]; ok {
		return b
	}
	b := &gbind{name: name, v: undefined, enum: true}
	g.m[name] = b
	g.order = append(g.order, b)
	return b
}

func (g *globalStore) lookup(name string) *gbind {
	b := g.m[name]
	if b == nil || !b.present {
		return nil
	}
	return b
}
