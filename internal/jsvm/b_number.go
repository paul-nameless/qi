package jsvm

import (
	"math"
	"math/big"
	"math/rand/v2"
	"strconv"
	"strings"
)

func (vm *VM) thisNum(this Value, name string) float64 {
	switch r := this.r.(type) {
	case nil:
		return this.n
	case *object:
		if r.class == classNumber {
			return r.inner.(float64)
		}
	}
	vm.throwTypeError("Number.prototype." + name + " requires that 'this' be a Number")
	return 0
}

func (vm *VM) setupNumber() {
	p := vm.numberProto
	p.class = classNumber
	p.inner = float64(0)
	toNum := func(vm *VM, args []Value) float64 {
		if len(args) == 0 {
			return 0
		}
		return vm.toNumber(args[0])
	}
	ctor := vm.newCtor("Number", 1, p, func(vm *VM, this Value, args []Value) Value {
		return num(toNum(vm, args))
	}, func(vm *VM, args []Value, nt *object) Value {
		return objV(&object{shape: vm.protoFrom(nt, p).rootShape(), class: classNumber, inner: toNum(vm, args)})
	})
	consts := map[string]float64{
		"MAX_SAFE_INTEGER": 9007199254740991, "MIN_SAFE_INTEGER": -9007199254740991, "EPSILON": 2.220446049250313e-16,
		"MAX_VALUE": math.MaxFloat64, "MIN_VALUE": 5e-324, "POSITIVE_INFINITY": math.Inf(1), "NEGATIVE_INFINITY": math.Inf(-1), "NaN": math.NaN(),
	}
	for _, k := range []string{"MAX_SAFE_INTEGER", "MIN_SAFE_INTEGER", "EPSILON", "MAX_VALUE", "MIN_VALUE", "POSITIVE_INFINITY", "NEGATIVE_INFINITY", "NaN"} {
		ctor.addProp(strKey(k), num(consts[k]), 0)
	}
	isNum := func(v Value) (float64, bool) { return v.n, v.r == nil }
	vm.method(ctor, "isInteger", 1, func(vm *VM, this Value, args []Value) Value {
		f, ok := isNum(arg(args, 0))
		return boolV(ok && !math.IsInf(f, 0) && f == math.Trunc(f))
	})
	vm.method(ctor, "isSafeInteger", 1, func(vm *VM, this Value, args []Value) Value {
		f, ok := isNum(arg(args, 0))
		return boolV(ok && f == math.Trunc(f) && math.Abs(f) <= 9007199254740991)
	})
	vm.method(ctor, "isFinite", 1, func(vm *VM, this Value, args []Value) Value {
		f, ok := isNum(arg(args, 0))
		return boolV(ok && !math.IsInf(f, 0) && f == f)
	})
	vm.method(ctor, "isNaN", 1, func(vm *VM, this Value, args []Value) Value {
		f, ok := isNum(arg(args, 0))
		return boolV(ok && f != f)
	})
	vm.setupNumberGlobals(ctor)
	vm.method(p, "toString", 1, func(vm *VM, this Value, args []Value) Value {
		f := vm.thisNum(this, "toString")
		radix := 10
		if r := arg(args, 0); !r.isUndef() {
			radix = int(toIntegerOrInf(vm.toNumber(r)))
			if radix < 2 || radix > 36 {
				vm.throwError("RangeError", "toString() radix must be between 2 and 36")
			}
		}
		if radix == 10 {
			return strV(numberToString(f))
		}
		return strV(radixString(f, radix))
	})
	vm.method(p, "toLocaleString", 0, func(vm *VM, this Value, args []Value) Value {
		f := vm.thisNum(this, "toLocaleString")
		if loc := arg(args, 0); !loc.isUndef() && vm.toString(loc) != "en-US" {
			vm.throwTypeError("unsupported: Number.prototype.toLocaleString locale " + vm.toString(loc))
		}
		return strV(vm.localeNumber(f, arg(args, 1)))
	})
	vm.method(p, "valueOf", 0, func(vm *VM, this Value, args []Value) Value {
		return num(vm.thisNum(this, "valueOf"))
	})
	vm.method(p, "toFixed", 1, func(vm *VM, this Value, args []Value) Value {
		f := vm.thisNum(this, "toFixed")
		d := toIntegerOrInf(vm.toNumber(arg(args, 0)))
		if d < 0 || d > 100 {
			vm.throwError("RangeError", "toFixed() digits argument must be between 0 and 100")
		}
		if f != f {
			return strV("NaN")
		}
		if math.Abs(f) >= 1e21 || math.IsInf(f, 0) {
			return strV(numberToString(f))
		}
		return strV(toFixed(f, int(d)))
	})
	vm.method(p, "toPrecision", 1, func(vm *VM, this Value, args []Value) Value {
		f := vm.thisNum(this, "toPrecision")
		if arg(args, 0).isUndef() {
			return strV(numberToString(f))
		}
		pr := toIntegerOrInf(vm.toNumber(arg(args, 0)))
		if f != f || math.IsInf(f, 0) {
			return strV(numberToString(f))
		}
		if pr < 1 || pr > 100 {
			vm.throwError("RangeError", "toPrecision() argument must be between 1 and 100")
		}
		return strV(toPrecision(f, int(pr)))
	})
	vm.method(p, "toExponential", 1, func(vm *VM, this Value, args []Value) Value {
		f := vm.thisNum(this, "toExponential")
		if f != f || math.IsInf(f, 0) {
			return strV(numberToString(f))
		}
		if arg(args, 0).isUndef() {
			if f == 0 {
				f = 0
			}
			return strV(expForm(strconv.FormatFloat(f, 'e', -1, 64)))
		}
		d := toIntegerOrInf(vm.toNumber(arg(args, 0)))
		if d < 0 || d > 100 {
			vm.throwError("RangeError", "toExponential() argument must be between 0 and 100")
		}
		return strV(expForm(roundExp(f, int(d))))
	})
	bp := vm.boolProto
	bp.class = classBoolean
	bp.inner = false
	vm.newCtor("Boolean", 1, bp, func(vm *VM, this Value, args []Value) Value {
		return boolV(toBool(arg(args, 0)))
	}, func(vm *VM, args []Value, nt *object) Value {
		return objV(&object{shape: vm.protoFrom(nt, bp).rootShape(), class: classBoolean, inner: toBool(arg(args, 0))})
	})
	thisBool := func(vm *VM, this Value) bool {
		if _, ok := this.r.(boolT); ok {
			return this.n != 0
		}
		if o, ok := this.r.(*object); ok && o.class == classBoolean {
			return o.inner.(bool)
		}
		vm.throwTypeError("Boolean.prototype method called on incompatible receiver")
		return false
	}
	vm.method(bp, "toString", 0, func(vm *VM, this Value, args []Value) Value {
		return strV(ToString(boolV(thisBool(vm, this))))
	})
	vm.method(bp, "valueOf", 0, func(vm *VM, this Value, args []Value) Value {
		return boolV(thisBool(vm, this))
	})
}

func (vm *VM) setupNumberGlobals(ctor *object) {
	pf := vm.method(ctor, "parseFloat", 1, func(vm *VM, this Value, args []Value) Value {
		return num(jsParseFloat(vm.toString(arg(args, 0))))
	})
	pi := vm.method(ctor, "parseInt", 2, func(vm *VM, this Value, args []Value) Value {
		return num(jsParseInt(vm.toString(arg(args, 0)), arg(args, 1), vm))
	})
	vm.setGlobal("parseFloat", objV(pf))
	vm.setGlobal("parseInt", objV(pi))
}

func exactDecimal(f float64) (string, string) {
	s := new(big.Float).SetPrec(2000).SetFloat64(math.Abs(f)).Text('f', 1100)
	ip, fp, _ := strings.Cut(s, ".")
	return ip, strings.TrimRight(fp, "0")
}

func roundDecimal(ip, fp string, d int) string {
	for len(fp) < d+1 {
		fp += "0"
	}
	digits := []byte(ip + fp[:d])
	if fp[d] >= '5' {
		i := len(digits) - 1
		for i >= 0 {
			if digits[i] == '9' {
				digits[i] = '0'
				i--
				continue
			}
			digits[i]++
			break
		}
		if i < 0 {
			digits = append([]byte{'1'}, digits...)
		}
	}
	n := len(digits) - d
	if d == 0 {
		return string(digits)
	}
	return string(digits[:n]) + "." + string(digits[n:])
}

func toFixed(f float64, d int) string {
	if f == 0 {
		f = 0
	}
	short := strconv.FormatFloat(math.Abs(f), 'f', -1, 64)
	if _, frac, _ := strings.Cut(short, "."); len(frac) != d+1 || frac[d] != '5' {
		return strconv.FormatFloat(f, 'f', d, 64)
	}
	ip, fp := exactDecimal(f)
	s := roundDecimal(ip, fp, d)
	if f < 0 {
		return "-" + s
	}
	return s
}

func roundExp(f float64, d int) string {
	if f == 0 {
		f = 0
	}
	if f == 0 {
		s := "0"
		if d > 0 {
			s += "." + strings.Repeat("0", d)
		}
		return s + "e+00"
	}
	ip, fp := exactDecimal(f)
	all := strings.TrimLeft(ip+fp, "0")
	exp := len(strings.TrimLeft(ip, "0")) - 1
	if strings.TrimLeft(ip, "0") == "" {
		exp = -(len(fp) - len(strings.TrimLeft(fp, "0"))) - 1
	}
	r := roundDecimal(all[:1], all[1:], d)
	if len(strings.Replace(r, ".", "", 1)) > d+1 {
		exp++
		r = roundDecimal(r[:1], strings.Replace(r[1:], ".", "", 1), d)
	}
	sign := ""
	if f < 0 {
		sign = "-"
	}
	es := strconv.Itoa(exp)
	if exp >= 0 {
		es = "+" + es
	}
	return sign + r + "e" + es
}

func expForm(s string) string {
	mant, exp, ok := strings.Cut(s, "e")
	if !ok {
		return s
	}
	sign := exp[0]
	digits := strings.TrimLeft(exp[1:], "0")
	if digits == "" {
		digits = "0"
	}
	return mant + "e" + string(sign) + digits
}

func toPrecision(f float64, p int) string {
	if f == 0 {
		s := "0"
		if p > 1 {
			s += "." + strings.Repeat("0", p-1)
		}
		return s
	}
	e := roundExp(f, p-1)
	mant, es, _ := strings.Cut(e, "e")
	exp, _ := strconv.Atoi(es)
	if exp < -6 || exp >= p {
		return expForm(mant + "e" + es)
	}
	neg := strings.HasPrefix(mant, "-")
	digits := strings.Replace(strings.TrimPrefix(mant, "-"), ".", "", 1)
	var s string
	if exp >= 0 {
		s = digits[:exp+1]
		if exp+1 < len(digits) {
			s += "." + digits[exp+1:]
		}
	} else {
		s = "0." + strings.Repeat("0", -exp-1) + digits
	}
	if neg {
		return "-" + s
	}
	return s
}

const radixDigits = "0123456789abcdefghijklmnopqrstuvwxyz"

func radixString(f float64, radix int) string {
	if f != f {
		return "NaN"
	}
	if math.IsInf(f, 0) {
		if f > 0 {
			return "Infinity"
		}
		return "-Infinity"
	}
	neg := f < 0
	f = math.Abs(f)
	integer := math.Floor(f)
	bi, _ := new(big.Float).SetFloat64(integer).Int(nil)
	frac, carry := radixFraction(f, radix)
	if carry {
		bi.Add(bi, big.NewInt(1))
	}
	out := bi.Text(radix)
	if frac != "" {
		out += "." + frac
	}
	if neg {
		return "-" + out
	}
	return out
}

func radixFraction(d float64, radix int) (string, bool) {
	bits := math.Float64bits(d)
	exp := int((bits >> 52) & 0x7FF)
	mant := bits & (1<<52 - 1)
	if exp == 0 {
		exp = 1
	} else {
		mant |= 1 << 52
	}
	e := exp - 1075
	if e >= 0 {
		return "", false
	}
	sh := uint(-e)
	s := new(big.Int).Lsh(big.NewInt(1), sh)
	b := new(big.Int).SetUint64(mant)
	b.Mod(b, s)
	if b.Sign() == 0 {
		return "", false
	}
	mlo := big.NewInt(1)
	mhi := big.NewInt(1)
	scale := uint(1)
	if mant == 1<<52 && exp > 1 {
		scale = 2
		mhi = big.NewInt(2)
	}
	s.Lsh(s, scale)
	b.Lsh(b, scale)
	odd := mant&1 == 1
	base := big.NewInt(int64(radix))
	var out []byte
	digit := new(big.Int)
	delta := new(big.Int)
	for {
		b.Mul(b, base)
		mlo.Mul(mlo, base)
		if mhi != mlo {
			mhi.Mul(mhi, base)
		}
		digit.QuoRem(b, s, b)
		dg := int(digit.Int64())
		j := b.Cmp(mlo)
		delta.Sub(s, mhi)
		j1 := 1
		if delta.Sign() > 0 {
			j1 = b.Cmp(delta)
		}
		done := false
		switch {
		case j1 == 0 && !odd:
			if j > 0 {
				dg++
			}
			done = true
		case j < 0 || (j == 0 && !odd):
			if j1 > 0 {
				b2 := new(big.Int).Lsh(b, 1)
				if b2.Cmp(s) > 0 {
					dg++
				}
			}
			done = true
		case j1 > 0:
			dg++
			done = true
		}
		out = append(out, byte(dg))
		if done || len(out) > 1100 {
			break
		}
	}
	carry := false
	for i := len(out) - 1; i >= 0 && int(out[i]) >= radix; i-- {
		out[i] = 0
		if i == 0 {
			carry = true
		} else {
			out[i-1]++
		}
	}
	for len(out) > 0 && out[len(out)-1] == 0 {
		out = out[:len(out)-1]
	}
	for i, d := range out {
		out[i] = radixDigits[d]
	}
	return string(out), carry
}

func (vm *VM) localeNumber(f float64, opts Value) string {
	if f != f {
		return "NaN"
	}
	if math.IsInf(f, 0) {
		if f > 0 {
			return "∞"
		}
		return "-∞"
	}
	minF, maxF := 0, 3
	if o, ok := opts.r.(*object); ok {
		for _, k := range o.ownKeys(true) {
			switch k.str() {
			case "minimumFractionDigits", "maximumFractionDigits":
			default:
				vm.throwTypeError("unsupported: Number.prototype.toLocaleString option " + k.str())
			}
		}
		digits := func(v Value) int {
			d := vm.toNumber(v)
			if d != d || d < 0 || d > 100 {
				vm.throwError("RangeError", "maximumFractionDigits value is out of range.")
			}
			return int(d)
		}
		if v := vm.getProp(o, strKey("minimumFractionDigits"), opts); !v.isUndef() {
			minF = digits(v)
			if maxF < minF {
				maxF = minF
			}
		}
		if v := vm.getProp(o, strKey("maximumFractionDigits"), opts); !v.isUndef() {
			maxF = digits(v)
		}
		if minF < 0 || minF > 100 || maxF < 0 || maxF > 100 || minF > maxF {
			vm.throwError("RangeError", "maximumFractionDigits value is out of range.")
		}
	}
	short := strconv.FormatFloat(math.Abs(f), 'f', -1, 64)
	sip, sfp, _ := strings.Cut(short, ".")
	s := roundDecimal(sip, sfp, maxF)
	if f < 0 {
		s = "-" + s
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	ip, fp, _ := strings.Cut(s, ".")
	for len(fp) > minF && strings.HasSuffix(fp, "0") {
		fp = fp[:len(fp)-1]
	}
	var b strings.Builder
	for i, c := range ip {
		if i > 0 && (len(ip)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	out := b.String()
	if fp != "" {
		out += "." + fp
	}
	if neg {
		return "-" + out
	}
	return out
}

func (vm *VM) setupMath() {
	m := vm.newObject()
	vm.setGlobal("Math", objV(m))
	for k, v := range map[string]float64{"PI": math.Pi, "E": math.E, "LN2": math.Ln2, "LN10": math.Ln10, "LOG2E": math.Log2E, "LOG10E": math.Log10E, "SQRT2": math.Sqrt2, "SQRT1_2": math.Sqrt2 / 2} {
		m.addProp(strKey(k), num(v), 0)
	}
	one := func(name string, fn func(float64) float64) {
		vm.method(m, name, 1, func(vm *VM, this Value, args []Value) Value {
			a := arg(args, 0)
			if a.r == nil {
				return num(fn(a.n))
			}
			return num(fn(vm.toNumber(a)))
		})
	}
	one("abs", math.Abs)
	one("ceil", math.Ceil)
	one("floor", math.Floor)
	one("trunc", math.Trunc)
	one("sqrt", math.Sqrt)
	one("cbrt", math.Cbrt)
	one("exp", math.Exp)
	one("expm1", math.Expm1)
	one("log", math.Log)
	one("log2", math.Log2)
	one("log10", math.Log10)
	one("log1p", math.Log1p)
	one("sin", math.Sin)
	one("cos", math.Cos)
	one("tan", math.Tan)
	one("asin", math.Asin)
	one("acos", math.Acos)
	one("atan", math.Atan)
	one("sinh", math.Sinh)
	one("cosh", math.Cosh)
	one("tanh", math.Tanh)
	one("asinh", math.Asinh)
	one("acosh", math.Acosh)
	one("atanh", math.Atanh)
	one("fround", func(f float64) float64 { return float64(float32(f)) })
	one("round", func(f float64) float64 {
		if f != f || math.IsInf(f, 0) {
			return f
		}
		r := math.Floor(f)
		if f-r >= 0.5 {
			r++
		}
		if r == 0 && (f < 0 || math.Signbit(f)) {
			return math.Copysign(0, -1)
		}
		return r
	})
	one("sign", func(f float64) float64 {
		switch {
		case f > 0:
			return 1
		case f < 0:
			return -1
		}
		return f
	})
	one("clz32", func(f float64) float64 {
		u := toUint32(f)
		n := 0
		for i := 31; i >= 0 && u&(1<<uint(i)) == 0; i-- {
			n++
		}
		return float64(n)
	})
	vm.method(m, "atan2", 2, func(vm *VM, this Value, args []Value) Value {
		return num(math.Atan2(vm.toNumber(arg(args, 0)), vm.toNumber(arg(args, 1))))
	})
	vm.method(m, "pow", 2, func(vm *VM, this Value, args []Value) Value {
		return num(jsPow(vm.toNumber(arg(args, 0)), vm.toNumber(arg(args, 1))))
	})
	vm.method(m, "imul", 2, func(vm *VM, this Value, args []Value) Value {
		return num(float64(toInt32(vm.toNumber(arg(args, 0))) * toInt32(vm.toNumber(arg(args, 1)))))
	})
	vm.method(m, "random", 0, func(vm *VM, this Value, args []Value) Value { return num(rand.Float64()) })
	vm.method(m, "max", 2, func(vm *VM, this Value, args []Value) Value {
		r := math.Inf(-1)
		nan := false
		for _, a := range args {
			f := vm.toNumeric(a)
			if f != f {
				nan = true
			}
			if f > r || (f == 0 && r == 0 && !math.Signbit(f)) {
				r = f
			}
		}
		if nan {
			return vNaN
		}
		return num(r)
	})
	vm.method(m, "min", 2, func(vm *VM, this Value, args []Value) Value {
		r := math.Inf(1)
		nan := false
		for _, a := range args {
			f := vm.toNumeric(a)
			if f != f {
				nan = true
			}
			if f < r || (f == 0 && r == 0 && math.Signbit(f)) {
				r = f
			}
		}
		if nan {
			return vNaN
		}
		return num(r)
	})
	vm.method(m, "hypot", 2, func(vm *VM, this Value, args []Value) Value {
		vals := make([]float64, len(args))
		maxAbs, nan := 0.0, false
		for i, a := range args {
			f := math.Abs(vm.toNumber(a))
			vals[i] = f
			if f != f {
				nan = true
			}
			maxAbs = math.Max(maxAbs, f)
		}
		if math.IsInf(maxAbs, 1) {
			return num(math.Inf(1))
		}
		if nan {
			return vNaN
		}
		if maxAbs == 0 {
			return num(0)
		}
		sum, comp := 0.0, 0.0
		for _, f := range vals {
			n := float64(f / maxAbs)
			summand := float64(float64(n*n) - comp)
			pre := float64(sum + summand)
			comp = float64(float64(pre-sum) - summand)
			sum = pre
		}
		return num(float64(math.Sqrt(sum) * maxAbs))
	})
}
