package notes

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/samahlstrom/retake/internal/jsonx"
)

// The legacy engine is Python 3.12. These helpers do the float arithmetic the way CPython does, so thresholds (shape
// detection, sentence gaps) and the printed times come out the same.

// pySum is CPython 3.12's sum() of floats: Neumaier-compensated.
func pySum(xs []float64) float64 {
	var f, c float64
	for _, x := range xs {
		t := f + x
		if math.Abs(f) >= math.Abs(x) {
			c += (f - t) + x
		} else {
			c += (x - t) + f
		}
		f = t
	}
	if c != 0 && !math.IsInf(c, 0) && !math.IsNaN(c) {
		f += c
	}
	return f
}

// pyMod is Python's float %: the result has the sign of the divisor.
func pyMod(x, y float64) float64 {
	m := math.Mod(x, y)
	if m != 0 {
		if (y < 0) != (m < 0) {
			m += y
		}
	} else {
		m = math.Copysign(0, y)
	}
	return m
}

// pyFloorDiv is Python's float //.
func pyFloorDiv(x, y float64) float64 {
	m := math.Mod(x, y)
	div := (x - m) / y
	if m != 0 && (y < 0) != (m < 0) {
		div--
	}
	if div == 0 {
		return math.Copysign(0, x/y)
	}
	fl := math.Floor(div)
	if div-fl > 0.5 {
		fl++
	}
	return fl
}

// vectorNorm is CPython's vector_norm, behind math.hypot and math.dist: accurate to within an ulp, often exact.
func vectorNorm(vec []float64) float64 {
	maxV, nan := 0.0, false
	for i, x := range vec {
		x = math.Abs(x)
		vec[i] = x
		if math.IsNaN(x) {
			nan = true
		}
		if x > maxV {
			maxV = x
		}
	}
	if math.IsInf(maxV, 0) {
		return maxV
	}
	if nan {
		return math.NaN()
	}
	if maxV == 0 || len(vec) <= 1 {
		return maxV
	}
	_, maxE := math.Frexp(maxV)
	if maxE < -1023 {
		for i := range vec {
			vec[i] /= dblMin // subnormals to normals
		}
		return dblMin * vectorNorm(vec)
	}
	scale := math.Ldexp(1, -maxE)
	csum, frac1, frac2 := 1.0, 0.0, 0.0
	for _, x := range vec {
		x *= scale
		hi, lo := dlMul(x, x)
		shi, slo := dlFastSum(csum, hi)
		csum = shi
		frac1 += lo
		frac2 += slo
	}
	h := math.Sqrt(csum - 1 + (frac1 + frac2))
	hi, lo := dlMul(-h, h)
	shi, slo := dlFastSum(csum, hi)
	csum = shi
	frac1 += lo
	frac2 += slo
	x := csum - 1 + (frac1 + frac2)
	h += x / (2 * h)
	return h / scale
}

const dblMin = 0x1p-1022 // C's DBL_MIN

func dlMul(x, y float64) (float64, float64) {
	z := x * y
	return z, math.FMA(x, y, -z)
}

func dlFastSum(a, b float64) (float64, float64) {
	z := a + b
	return z, (a - z) + b
}

// hypot is Python's math.hypot(x, y).
func hypot(x, y float64) float64 { return vectorNorm([]float64{x, y}) }

// dist is Python's math.dist for two points.
func dist(p, q [2]float64) float64 { return vectorNorm([]float64{p[0] - q[0], p[1] - q[1]}) }

// pi64 is math.Pi rounded to a float64 first, so radToDeg is C's 180.0 / M_PI, as CPython computes it.
var pi64 = math.Pi

var radToDeg = 180 / pi64

// r3 is Python's round(x, 3).
func r3(x float64) float64 { return jsonx.Round3(x) }

// num reads a JSON number; ok is false for anything else (null, missing, strings).
func num(raw json.RawMessage) (float64, bool) {
	if f, ok := jsonx.Number(raw); ok {
		return f, true
	}
	if jsonx.Kind(raw) == "bool" { // Python: True == 1
		if bytes.Equal(bytes.TrimSpace(raw), []byte("true")) {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

// numOr reads a JSON number or returns def.
func numOr(raw json.RawMessage, def float64) float64 {
	if f, ok := num(raw); ok {
		return f
	}
	return def
}

// str reads a JSON string; non-strings give Python's str() of the value (missing = "").
func str(raw json.RawMessage) string {
	if jsonx.Kind(raw) == "string" {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	return pyStr(raw)
}

// pyStr is Python's str() of a decoded JSON value: None, True, 30.0, 1920, text.
func pyStr(raw json.RawMessage) string {
	s := bytes.TrimSpace(raw)
	switch jsonx.Kind(s) {
	case "", "null":
		return "None"
	case "bool":
		if string(s) == "true" {
			return "True"
		}
		return "False"
	case "string":
		var v string
		if json.Unmarshal(s, &v) == nil {
			return v
		}
	case "number":
		if !bytes.ContainsAny(s, ".eE") {
			if i, err := strconv.ParseInt(string(s), 10, 64); err == nil {
				return strconv.FormatInt(i, 10)
			}
			return string(s)
		}
		if f, err := strconv.ParseFloat(string(s), 64); err == nil {
			return jsonx.FormatFloat(f)
		}
	}
	return string(s)
}

// pyKey is a comparable key for a JSON value with Python's == semantics on numbers (1 == 1.0 == True).
func pyKey(raw json.RawMessage) string {
	if f, ok := num(raw); ok {
		return "n:" + strconv.FormatFloat(f, 'g', -1, 64)
	}
	if jsonx.Kind(raw) == "string" {
		return "s:" + str(raw)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return "null"
	}
	return string(bytes.TrimSpace(raw))
}

// truthy is Python truthiness of a JSON value; missing is falsy.
func truthy(raw json.RawMessage) bool { return jsonx.Truthy(raw) }

// isNull reports a missing or null value (Python's `is None` after .get()).
func isNull(raw json.RawMessage) bool {
	k := jsonx.Kind(raw)
	return k == "" || k == "null"
}

// collapseSpace is re.sub(r"\s+", " ", s).strip().
func collapseSpace(s string) string { return strings.Join(strings.Fields(s), " ") }
