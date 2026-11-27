package runtime

func complexIsNaN(f float64) bool { return f != f }

func complexIsInf(f float64) bool {
	if complexIsNaN(f) {
		return false
	}
	if f < 0 {
		f = -f
	}
	return f > 1.7976931348623157e+308
}

func complexAbs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// complexSignbit reports the sign bit, including for ±0.
func complexSignbit(f float64) bool {
	if f < 0 {
		return true
	}
	if f != 0 {
		return false
	}
	one := 1.0
	return one/f < 0
}

func complexCopysign(x, y float64) float64 {
	if complexSignbit(x) == complexSignbit(y) {
		return x
	}
	return -x
}

func complexInf2one(f float64) float64 {
	g := 0.0
	if complexIsInf(f) {
		g = 1.0
	}
	return complexCopysign(g, f)
}

func complexIsFinite(f float64) bool {
	return !complexIsNaN(f) && !complexIsInf(f)
}

func complexPosInf() float64 {
	max := 1.7976931348623157e+308
	return max * max
}

// complex128div implements robust complex division (Smith + C99 G.5.1).
func complex128div(n, m complex128) complex128 {
	var e, f float64
	inf := complexPosInf()

	if complexAbs(real(m)) >= complexAbs(imag(m)) {
		ratio := imag(m) / real(m)
		denom := real(m) + ratio*imag(m)
		e = (real(n) + imag(n)*ratio) / denom
		f = (imag(n) - real(n)*ratio) / denom
	} else {
		ratio := real(m) / imag(m)
		denom := imag(m) + ratio*real(m)
		e = (real(n)*ratio + imag(n)) / denom
		f = (imag(n)*ratio - real(n)) / denom
	}

	if complexIsNaN(e) && complexIsNaN(f) {
		a, b := real(n), imag(n)
		c, d := real(m), imag(m)
		switch {
		case m == 0 && (!complexIsNaN(a) || !complexIsNaN(b)):
			e = complexCopysign(inf, c) * a
			f = complexCopysign(inf, c) * b
		case (complexIsInf(a) || complexIsInf(b)) && complexIsFinite(c) && complexIsFinite(d):
			a = complexInf2one(a)
			b = complexInf2one(b)
			e = inf * (a*c + b*d)
			f = inf * (b*c - a*d)
		case (complexIsInf(c) || complexIsInf(d)) && complexIsFinite(a) && complexIsFinite(b):
			c = complexInf2one(c)
			d = complexInf2one(d)
			e = 0 * (a*c + b*d)
			f = 0 * (b*c - a*d)
		}
	}
	return complex(e, f)
}
