package jsvm

import (
	"fmt"
	"math"
	"strings"
	"time"
)

const msPerDay = 86400000

type dateParts struct {
	y, mo, d, h, mi, s, ms, wd float64
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func daysFromCivil(y, m, d int64) int64 {
	if m <= 2 {
		y--
	}
	era := floorDiv(y, 400)
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

func civilFromDays(z int64) (int64, int64, int64) {
	z += 719468
	era := floorDiv(z, 146097)
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y := yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d := doy - (153*mp+2)/5 + 1
	m := mp + 3
	if mp >= 10 {
		m = mp - 9
	}
	if m <= 2 {
		y++
	}
	return y, m, d
}

func finite(fs ...float64) bool {
	for _, f := range fs {
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return false
		}
	}
	return true
}

func makeDay(year, month, date float64) float64 {
	if !finite(year, month, date) {
		return math.NaN()
	}
	y, m, d := math.Trunc(year), math.Trunc(month), math.Trunc(date)
	ym := y + math.Floor(m/12)
	if math.Abs(ym) > 1e6 {
		return math.NaN()
	}
	mn := math.Mod(m, 12)
	if mn < 0 {
		mn += 12
	}
	return float64(daysFromCivil(int64(ym), int64(mn)+1, 1)) + d - 1
}

func makeTime(h, m, s, ms float64) float64 {
	if !finite(h, m, s, ms) {
		return math.NaN()
	}
	return math.Trunc(h)*3600000 + math.Trunc(m)*60000 + math.Trunc(s)*1000 + math.Trunc(ms)
}

func makeDate(day, t float64) float64 {
	if !finite(day, t) {
		return math.NaN()
	}
	return day*msPerDay + t
}

func timeClip(t float64) float64 {
	if !finite(t) || math.Abs(t) > 8.64e15 {
		return math.NaN()
	}
	return math.Trunc(t) + 0
}

func splitTime(t float64) dateParts {
	day := math.Floor(t / msPerDay)
	rem := t - day*msPerDay
	y, m, d := civilFromDays(int64(day))
	wd := math.Mod(day+4, 7)
	if wd < 0 {
		wd += 7
	}
	return dateParts{
		y: float64(y), mo: float64(m - 1), d: float64(d),
		h: math.Floor(rem / 3600000), mi: math.Mod(math.Floor(rem/60000), 60),
		s: math.Mod(math.Floor(rem/1000), 60), ms: math.Mod(rem, 1000), wd: wd,
	}
}

func (p dateParts) time() float64 {
	return makeDate(makeDay(p.y, p.mo, p.d), makeTime(p.h, p.mi, p.s, p.ms))
}

func zoneAt(t float64) (string, float64) {
	if !finite(t) {
		return "UTC", 0
	}
	name, off := time.UnixMilli(int64(t)).In(time.Local).Zone()
	return name, float64(off) * 1000
}

func localOffset(t float64) float64 {
	_, off := zoneAt(t)
	return off
}

func localTime(t float64) float64 { return t + localOffset(t) }

func utcFromLocal(t float64) float64 {
	if !finite(t) {
		return math.NaN()
	}
	before := localOffset(t - 12*3600000)
	after := localOffset(t + 12*3600000)
	u1, u2 := t-before, t-after
	ok1, ok2 := localOffset(u1) == before, localOffset(u2) == after
	switch {
	case ok1 && ok2:
		return math.Min(u1, u2)
	case ok1:
		return u1
	case ok2:
		return u2
	}
	return u1
}

func (vm *VM) thisDate(this Value, name string) *object {
	if o, ok := this.r.(*object); ok && o.class == classDate {
		return o
	}
	vm.throwTypeError("this is not a Date object.")
	return nil
}

func dateValue(o *object) float64 { return o.inner.(float64) }

func (vm *VM) newDate(t float64, proto *object) *object {
	if proto == nil {
		proto = vm.dateProto
	}
	vm.track(64)
	return &object{shape: proto.rootShape(), class: classDate, inner: timeClip(t)}
}

func nowMs() float64 { return float64(time.Now().UnixMilli()) }

func (vm *VM) dateFromArgs(args []Value, local bool) float64 {
	f := [7]float64{math.NaN(), 0, 1, 0, 0, 0, 0}
	for i := 0; i < 7 && i < len(args); i++ {
		f[i] = vm.toNumber(args[i])
	}
	if finite(f[0]) {
		if y := math.Trunc(f[0]); y >= 0 && y <= 99 {
			f[0] = 1900 + y
		}
	}
	t := makeDate(makeDay(f[0], f[1], f[2]), makeTime(f[3], f[4], f[5], f[6]))
	if local {
		t = utcFromLocal(t)
	}
	return timeClip(t)
}

func (vm *VM) setupDate() {
	p := vm.newObject()
	vm.dateProto = p
	build := func(vm *VM, args []Value, nt *object) Value {
		proto := vm.protoFrom(nt, p)
		switch len(args) {
		case 0:
			return objV(vm.newDate(nowMs(), proto))
		case 1:
			if o, ok := args[0].r.(*object); ok && o.class == classDate {
				return objV(vm.newDate(dateValue(o), proto))
			}
			v := vm.toPrimitive(args[0], "")
			if s, ok := v.r.(*jstr); ok {
				return objV(vm.newDate(parseDate(s.flat()), proto))
			}
			return objV(vm.newDate(vm.toNumber(v), proto))
		}
		return objV(vm.newDate(vm.dateFromArgs(args, true), proto))
	}
	ctor := vm.newCtor("Date", 7, p, func(vm *VM, this Value, args []Value) Value {
		return strV(dateString(nowMs(), true, true))
	}, build)
	vm.method(ctor, "now", 0, func(vm *VM, this Value, args []Value) Value { return num(nowMs()) })
	vm.method(ctor, "parse", 1, func(vm *VM, this Value, args []Value) Value {
		return num(parseDate(vm.toString(arg(args, 0))))
	})
	vm.method(ctor, "UTC", 7, func(vm *VM, this Value, args []Value) Value {
		return num(vm.dateFromArgs(args, false))
	})
	vm.dateGetters(p)
	vm.dateSetters(p)
	vm.dateFormatters(p)
}

func (vm *VM) dateGetters(p *object) {
	get := func(name string, local bool, field func(dateParts) float64) {
		vm.method(p, name, 0, func(vm *VM, this Value, args []Value) Value {
			t := dateValue(vm.thisDate(this, name))
			if math.IsNaN(t) {
				return vNaN
			}
			if local {
				t = localTime(t)
			}
			return num(field(splitTime(t)))
		})
	}
	fields := []struct {
		name string
		f    func(dateParts) float64
	}{
		{"FullYear", func(p dateParts) float64 { return p.y }},
		{"Month", func(p dateParts) float64 { return p.mo }},
		{"Date", func(p dateParts) float64 { return p.d }},
		{"Day", func(p dateParts) float64 { return p.wd }},
		{"Hours", func(p dateParts) float64 { return p.h }},
		{"Minutes", func(p dateParts) float64 { return p.mi }},
		{"Seconds", func(p dateParts) float64 { return p.s }},
		{"Milliseconds", func(p dateParts) float64 { return p.ms }},
	}
	for _, fl := range fields {
		get("get"+fl.name, true, fl.f)
		get("getUTC"+fl.name, false, fl.f)
	}
	timeValue := func(vm *VM, this Value, args []Value) Value {
		return num(dateValue(vm.thisDate(this, "getTime")))
	}
	vm.method(p, "getTime", 0, timeValue)
	vm.method(p, "valueOf", 0, timeValue)
	vm.method(p, "getTimezoneOffset", 0, func(vm *VM, this Value, args []Value) Value {
		t := dateValue(vm.thisDate(this, "getTimezoneOffset"))
		if math.IsNaN(t) {
			return vNaN
		}
		return num(-localOffset(t)/60000 + 0)
	})
}

func (vm *VM) dateSetters(p *object) {
	set := func(name string, first, count int, local bool) {
		vm.method(p, name, count, func(vm *VM, this Value, args []Value) Value {
			o := vm.thisDate(this, name)
			t := dateValue(o)
			vals := make([]float64, 0, count)
			for i := 0; i < count && i < len(args); i++ {
				vals = append(vals, vm.toNumber(args[i]))
			}
			if len(vals) == 0 {
				vals = append(vals, math.NaN())
			}
			if math.IsNaN(t) {
				if first != 0 {
					return vNaN
				}
				t = 0
			} else if local {
				t = localTime(t)
			}
			parts := splitTime(t)
			slots := []*float64{&parts.y, &parts.mo, &parts.d, &parts.h, &parts.mi, &parts.s, &parts.ms}
			for i, v := range vals {
				*slots[first+i] = v
			}
			nt := parts.time()
			if local {
				nt = utcFromLocal(nt)
			}
			nt = timeClip(nt)
			o.inner = nt
			return num(nt)
		})
	}
	for _, s := range []struct {
		name         string
		first, count int
	}{
		{"FullYear", 0, 3}, {"Month", 1, 2}, {"Date", 2, 1}, {"Hours", 3, 4},
		{"Minutes", 4, 3}, {"Seconds", 5, 2}, {"Milliseconds", 6, 1},
	} {
		set("set"+s.name, s.first, s.count, true)
		set("setUTC"+s.name, s.first, s.count, false)
	}
	vm.method(p, "setTime", 1, func(vm *VM, this Value, args []Value) Value {
		o := vm.thisDate(this, "setTime")
		t := timeClip(vm.toNumber(arg(args, 0)))
		o.inner = t
		return num(t)
	})
}

var weekdays = [7]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
var months = [12]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

func yearString(y float64) string {
	if y < 0 {
		return fmt.Sprintf("-%06d", int64(-y))
	}
	return fmt.Sprintf("%04d", int64(y))
}

func isoString(t float64) string {
	p := splitTime(t)
	y := int64(p.y)
	var ys string
	switch {
	case y >= 0 && y <= 9999:
		ys = fmt.Sprintf("%04d", y)
	case y < 0:
		ys = fmt.Sprintf("-%06d", -y)
	default:
		ys = fmt.Sprintf("+%06d", y)
	}
	return fmt.Sprintf("%s-%02d-%02dT%02d:%02d:%02d.%03dZ", ys, int(p.mo)+1, int(p.d), int(p.h), int(p.mi), int(p.s), int(p.ms))
}

func zoneSuffix(t float64) string {
	name, off := zoneAt(t)
	sign := '+'
	if off < 0 {
		sign = '-'
		off = -off
	}
	mins := int64(off / 60000)
	s := fmt.Sprintf("GMT%c%02d%02d", sign, mins/60, mins%60)
	if name != "" {
		s += " (" + name + ")"
	}
	return s
}

func dateString(t float64, date, tm bool) string {
	if math.IsNaN(t) {
		return "Invalid Date"
	}
	p := splitTime(localTime(t))
	var parts []string
	if date {
		parts = append(parts, fmt.Sprintf("%s %s %02d %s", weekdays[int(p.wd)], months[int(p.mo)], int(p.d), yearString(p.y)))
	}
	if tm {
		parts = append(parts, fmt.Sprintf("%02d:%02d:%02d %s", int(p.h), int(p.mi), int(p.s), zoneSuffix(t)))
	}
	return strings.Join(parts, " ")
}

func (vm *VM) dateFormatters(p *object) {
	str := func(name string, fn func(t float64) string) {
		vm.method(p, name, 0, func(vm *VM, this Value, args []Value) Value {
			return strV(fn(dateValue(vm.thisDate(this, name))))
		})
	}
	str("toString", func(t float64) string { return dateString(t, true, true) })
	str("toDateString", func(t float64) string { return dateString(t, true, false) })
	str("toTimeString", func(t float64) string { return dateString(t, false, true) })
	str("toUTCString", func(t float64) string {
		if math.IsNaN(t) {
			return "Invalid Date"
		}
		q := splitTime(t)
		return fmt.Sprintf("%s, %02d %s %s %02d:%02d:%02d GMT", weekdays[int(q.wd)], int(q.d), months[int(q.mo)], yearString(q.y), int(q.h), int(q.mi), int(q.s))
	})
	vm.method(p, "toISOString", 0, func(vm *VM, this Value, args []Value) Value {
		t := dateValue(vm.thisDate(this, "toISOString"))
		if math.IsNaN(t) {
			vm.throwError("RangeError", "Invalid time value")
		}
		return strV(isoString(t))
	})
	vm.method(p, "toJSON", 1, func(vm *VM, this Value, args []Value) Value {
		tv := vm.toPrimitive(this, "number")
		if tv.r == nil && !finite(tv.n) {
			return null
		}
		return vm.invoke(this, "toISOString")
	})
	vm.method(p, "toLocaleDateString", 0, func(vm *VM, this Value, args []Value) Value {
		t := dateValue(vm.thisDate(this, "toLocaleDateString"))
		if math.IsNaN(t) {
			return strV("Invalid Date")
		}
		if loc := arg(args, 0); !loc.isUndef() && vm.toString(loc) != "en-US" {
			vm.throwTypeError("unsupported: toLocaleDateString locale " + vm.toString(loc))
		}
		if !vm.utcOption(arg(args, 1)) {
			t = localTime(t)
		}
		q := splitTime(t)
		return strV(fmt.Sprintf("%02d/%02d/%s", int(q.mo)+1, int(q.d), yearString(q.y)))
	})
	for _, name := range []string{"toLocaleString", "toLocaleTimeString"} {
		n := name
		vm.method(p, n, 0, func(vm *VM, this Value, args []Value) Value {
			vm.throwTypeError("unsupported: Date.prototype." + n)
			return undefined
		})
	}
}

func (vm *VM) utcOption(opts Value) bool {
	o, ok := opts.r.(*object)
	if !ok {
		return false
	}
	for _, k := range o.ownKeys(true) {
		if k.str() != "timeZone" {
			vm.throwTypeError("unsupported: toLocaleDateString option " + k.str())
		}
	}
	tzv := vm.getProp(o, strKey("timeZone"), opts)
	if tzv.isUndef() {
		return false
	}
	tz := vm.toString(tzv)
	switch strings.ToUpper(tz) {
	case "UTC", "ETC/UTC", "GMT":
		return true
	}
	vm.throwTypeError("unsupported: toLocaleDateString timeZone " + tz)
	return false
}
