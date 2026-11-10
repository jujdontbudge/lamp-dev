package plugin

import (
	"encoding/json"
	"errors"
	"math/big"
	"regexp"
	"strings"
)

var decimalSyntax = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// decimal stores a normalized significand and decimal order, never an expanded
// power of ten. Memory stays proportional to the bounded JSON token even for
// exponents such as 1e1000000000. No binary floating-point conversion occurs.
type decimal struct {
	sign     int
	digits   string
	order    big.Int
	integral bool
}

func parseDecimal(n json.Number) (decimal, error) {
	s := n.String()
	var out decimal
	if len(s) > MaxFrameBytes || !decimalSyntax.MatchString(s) {
		return out, errors.New("invalid exact decimal")
	}
	out.sign = 1
	if s[0] == '-' {
		out.sign = -1
		s = s[1:]
	}
	var exponent big.Int
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		if _, ok := exponent.SetString(s[i+1:], 10); !ok {
			return out, errors.New("invalid decimal exponent")
		}
		s = s[:i]
	}
	fraction := 0
	if i := strings.IndexByte(s, '.'); i >= 0 {
		fraction = len(s) - i - 1
		s = s[:i] + s[i+1:]
	}
	s = strings.TrimLeft(s, "0")
	if s == "" {
		out.sign = 0
		out.integral = true
		return out, nil
	}
	out.digits = strings.TrimRight(s, "0")
	exponent.Add(&exponent, big.NewInt(int64(len(s)-len(out.digits)-fraction)))
	out.integral = exponent.Sign() >= 0
	out.order.Add(&exponent, big.NewInt(int64(len(out.digits))))
	return out, nil
}
func (a decimal) compare(b decimal) int {
	if a.sign != b.sign {
		if a.sign < b.sign {
			return -1
		}
		return 1
	}
	if a.sign == 0 {
		return 0
	}
	if cmp := a.order.Cmp(&b.order); cmp != 0 {
		return a.sign * cmp
	}
	for i := 0; i < max(len(a.digits), len(b.digits)); i++ {
		x, y := byte('0'), byte('0')
		if i < len(a.digits) {
			x = a.digits[i]
		}
		if i < len(b.digits) {
			y = b.digits[i]
		}
		if x < y {
			return -a.sign
		}
		if x > y {
			return a.sign
		}
	}
	return 0
}
