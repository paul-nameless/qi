package jsvm

import (
	"sort"
	"strconv"
	"strings"
)

type source struct {
	name  string
	src   string
	lines []int
}

func newSource(name, src string) *source {
	s := &source{name: name, src: src, lines: []int{0}}
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			s.lines = append(s.lines, i+1)
		}
	}
	return s
}

func (s *source) position(pos int) (int, int) {
	line := max(sort.Search(len(s.lines), func(i int) bool { return s.lines[i] > pos })-1, 0)
	start := s.lines[line]
	end := min(pos, len(s.src))
	col := utf16Len(s.src[start:end]) + 1
	return line + 1, col
}

type Exception struct {
	Name    string
	Message string
	Stack   string
	Line    int
	Column  int
	Value   Value
}

func (e *Exception) Error() string {
	head := e.Message
	if e.Name != "" {
		head = e.Name + ": " + e.Message
	}
	frames := e.Stack
	if !strings.HasPrefix(frames, "    at ") {
		frames = ""
		if i := strings.Index(e.Stack, "\n    at "); i >= 0 {
			frames = e.Stack[i+1:]
		}
	}
	if frames != "" {
		return head + "\n" + frames
	}
	return head
}

type stackFrame struct {
	code *funcCode
	pos  int32
}

type errorData struct {
	head   string
	frames []stackFrame
	done   bool
}

func (ed *errorData) position() (int, int) {
	if len(ed.frames) == 0 {
		return 0, 0
	}
	f := ed.frames[0]
	return f.code.src.position(int(f.pos))
}

func (ed *errorData) render() string {
	var b strings.Builder
	b.WriteString(ed.head)
	for _, f := range ed.frames {
		line, col := f.code.src.position(int(f.pos))
		b.WriteString("\n    at ")
		loc := f.code.src.name + ":" + strconv.Itoa(line) + ":" + strconv.Itoa(col)
		if f.code.name != "" {
			b.WriteString(f.code.name)
			b.WriteString(" (")
			b.WriteString(loc)
			b.WriteString(")")
		} else {
			b.WriteString(loc)
		}
	}
	return b.String()
}

func (vm *VM) errorStack(o *object) (Value, bool) {
	ed, ok := o.inner.(*errorData)
	if !ok || ed.done {
		return undefined, false
	}
	ed.done = true
	v := strV(ed.render())
	o.defineHidden("stack", v)
	return v, true
}

func (vm *VM) makeError(kind, msg string) *object {
	proto := vm.errorProtos[kind]
	if proto == nil {
		proto = vm.errorProto
	}
	o := &object{shape: proto.rootShape(), class: classError}
	o.defineHidden("message", strV(msg))
	vm.attachStack(o, kind, msg)
	return o
}

func (vm *VM) namedError(name, msg string) *object {
	o := vm.makeError(name, msg)
	if vm.errorProtos[name] == nil && name != "Error" {
		o.defineHidden("name", strV(name))
		if ed, ok := o.inner.(*errorData); ok {
			ed.head = errorHead(name, msg)
		}
	}
	return o
}

func errorHead(name, msg string) string {
	if msg == "" {
		return name
	}
	return name + ": " + msg
}

func (vm *VM) attachStack(o *object, name, msg string) {
	ed := &errorData{head: errorHead(name, msg)}
	for f := vm.top; f != nil && len(ed.frames) < 50; f = f.parent {
		if f.code != nil && f.code.src != nil {
			ed.frames = append(ed.frames, stackFrame{f.code, f.pos})
		}
	}
	o.inner = ed
}

func (vm *VM) exceptionFrom(v Value) *Exception {
	e := &Exception{Value: v}
	o, ok := v.r.(*object)
	if !ok {
		e.Name = ""
		e.Message = "Uncaught " + vm.safeString(v)
		return e
	}
	name, msg := vm.safeGet(o, "name"), vm.safeGet(o, "message")
	if o.class != classError && name.isUndef() && msg.isUndef() {
		e.Message = "Uncaught " + vm.safeString(v)
		return e
	}
	e.Name = vm.safeString(name)
	e.Message = vm.safeString(msg)
	if s, ok := vm.safeGet(o, "stack").r.(*jstr); ok {
		e.Stack = s.flat()
	}
	if ed, ok := o.inner.(*errorData); ok {
		e.Line, e.Column = ed.position()
	}
	return e
}

func (vm *VM) safeGet(o *object, name string) Value {
	v := undefined
	vm.protectAll(func() { v = vm.getProp(o, strKey(name), objV(o)) })
	return v
}

func (vm *VM) safeString(v Value) string {
	if s, ok := v.r.(*jstr); ok {
		return s.flat()
	}
	if _, ok := v.r.(*object); !ok {
		return ToString(v)
	}
	s := ToString(v)
	vm.protectAll(func() { s = vm.toString(v) })
	return s
}

func (vm *VM) protectAll(fn func()) {
	st := vm.save()
	defer func() {
		if r := recover(); r != nil {
			vm.restore(st)
			if vm.top != nil && isUnwind(r) {
				panic(r)
			}
		}
	}()
	fn()
}

func isUnwind(r any) bool {
	switch r.(type) {
	case *interruptSignal, killSignal:
		return true
	}
	return false
}

func (vm *VM) syntaxException(src *source, se *syntaxErr) *Exception {
	line, col := src.position(se.pos)
	return &Exception{
		Name:    "SyntaxError",
		Message: se.msg,
		Stack:   "SyntaxError: " + se.msg + "\n    at " + src.name + ":" + strconv.Itoa(line) + ":" + strconv.Itoa(col),
		Line:    line,
		Column:  col,
	}
}
