// SPDX-License-Identifier: AGPL-3.0-only

// Package money implements exact decimal arithmetic for budgets: parsing from
// decimal text, half-up rounding to a currency's minor unit, the budget and
// payment-schedule algorithms, and locale formatting. float64 is forbidden here.
package money

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cockroachdb/apd/v3"
)

// Decimal is an exact decimal value.
type Decimal = apd.Decimal

// maxIntegerDigits bounds magnitudes so that every product and sum in the
// budget algorithm stays exact within the 34-digit context (12+12 integer
// digits plus 4+4 decimals ≤ 34).
const maxIntegerDigits = 12

var calc = apd.Context{
	Precision:   34,
	Rounding:    apd.RoundHalfUp,
	MaxExponent: apd.MaxExponent,
	MinExponent: apd.MinExponent,
	Traps:       apd.DefaultTraps,
}

var (
	hundred = apd.New(100, 0)
	zero    = apd.New(0, 0)
)

// Hundred returns a new decimal 100 (percentage bound).
func Hundred() *Decimal { return apd.New(100, 0) }

// ErrInvalid wraps every parse/limit failure; messages are agent-facing.
var ErrInvalid = errors.New("invalid amount")

// Parse reads a non-negative decimal from a JSON string or json.Number,
// allowing at most maxDecimals fractional digits (after removing trailing
// zeros). Any other Go type (including float64) is rejected.
func Parse(v any, maxDecimals int) (*Decimal, error) {
	var text string
	switch x := v.(type) {
	case string:
		text = strings.TrimSpace(x)
	case json.Number:
		text = x.String()
	default:
		return nil, fmt.Errorf("%w: must be a JSON number or a decimal string", ErrInvalid)
	}
	d, _, err := apd.NewFromString(text)
	if err != nil || d.Form != apd.Finite {
		return nil, fmt.Errorf("%w: %q is not a decimal number", ErrInvalid, text)
	}
	if d.Negative && !d.IsZero() {
		return nil, fmt.Errorf("%w: %q must be ≥ 0", ErrInvalid, text)
	}
	d.Negative = false
	var r Decimal
	r.Reduce(d)
	if r.Exponent < 0 && int(-r.Exponent) > maxDecimals {
		return nil, fmt.Errorf("%w: %q has more than %d decimals", ErrInvalid, text, maxDecimals)
	}
	if intDigits(&r) > maxIntegerDigits {
		return nil, fmt.Errorf("%w: %q is too large", ErrInvalid, text)
	}
	return &r, nil
}

func intDigits(d *Decimal) int {
	n := int(d.NumDigits()) + int(d.Exponent)
	if n < 0 {
		return 0
	}
	return n
}

// arithError is raised by the internal helpers below; magnitudes are bounded
// by Parse so it is unreachable for validated input. Exported functions turn it
// into an error with recoverArith, so external input can never panic the process.
type arithError struct{ err error }

func check(err error) {
	if err != nil {
		panic(arithError{err})
	}
}

func recoverArith(err *error) {
	if r := recover(); r != nil {
		ae, ok := r.(arithError)
		if !ok {
			panic(r)
		}
		*err = fmt.Errorf("%w: arithmetic failure: %w", ErrInvalid, ae.err)
	}
}

// Round rounds d half-up to minor fractional digits.
func Round(d *Decimal, minor int) (_ *Decimal, err error) {
	defer recoverArith(&err)
	return round(d, minor), nil
}

func round(d *Decimal, minor int) *Decimal {
	var r Decimal
	_, err := calc.Quantize(&r, d, -int32(minor)) //nolint:gosec // minor is 0..4 from the ISO 4217 table
	check(err)
	return &r
}

func mul(a, b *Decimal) *Decimal {
	var r Decimal
	_, err := calc.Mul(&r, a, b)
	check(err)
	return &r
}

func add(a, b *Decimal) *Decimal {
	var r Decimal
	_, err := calc.Add(&r, a, b)
	check(err)
	return &r
}

func sub(a, b *Decimal) *Decimal {
	var r Decimal
	_, err := calc.Sub(&r, a, b)
	check(err)
	return &r
}

// factor returns (1 - pct/100); pct is a validated value in [0, 100].
func factor(pct *Decimal) *Decimal {
	var q Decimal
	_, err := calc.Quo(&q, pct, hundred)
	check(err)
	return sub(apd.New(1, 0), &q)
}

// pctOf returns d × pct / 100 (unrounded).
func pctOf(d, pct *Decimal) *Decimal {
	var q Decimal
	_, err := calc.Quo(&q, mul(d, pct), hundred)
	check(err)
	return &q
}
