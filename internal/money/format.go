// SPDX-License-Identifier: AGPL-3.0-only

package money

import "strings"

// numberStyle is the separator convention for a language.
type numberStyle struct{ group, decimal string }

func styleFor(lang string) (numberStyle, bool) {
	switch lang {
	case "es", "ca":
		return numberStyle{group: ".", decimal: ","}, true
	default: // "en" and every language without built-in formatting
		return numberStyle{group: ",", decimal: "."}, false
	}
}

// FormatAmount formats d (rounded half-up to the currency minor unit) for a
// BCP-47 primary language tag:
//
//	en (and fallback): €1,234.56   CHF 1,234.56   -€12.00
//	es, ca:            1.234,56 €  1.234,56 CHF   -12,00 €
func FormatAmount(d *Decimal, cur Currency, lang string) (_ string, err error) {
	defer recoverArith(&err)
	st, suffix := styleFor(lang)
	r := round(d, cur.MinorUnits)
	neg := r.Negative && !r.IsZero()
	num := group(r.Text('f'), st)
	sym := cur.Symbol
	var s string
	switch {
	case suffix && sym != "":
		s = num + " " + sym
	case suffix:
		s = num + " " + cur.Code
	case sym != "":
		s = sym + num
	default:
		s = cur.Code + " " + num
	}
	if neg {
		s = "-" + s
	}
	return s, nil
}

// FormatNumber formats a non-money decimal (quantities) with the language's
// separators, keeping its own decimals: en "1,234.5", es "1.234,5".
func FormatNumber(d *Decimal, lang string) string {
	st, _ := styleFor(lang)
	var r Decimal
	r.Reduce(d)
	return group(r.Text('f'), st)
}

// FormatPercent formats a percentage: en "21%", es/ca "21 %".
func FormatPercent(d *Decimal, lang string) string {
	if _, suffix := styleFor(lang); suffix {
		return FormatNumber(d, lang) + " %"
	}
	return FormatNumber(d, lang) + "%"
}

// group inserts group separators into plain decimal text "-1234.56".
func group(text string, st numberStyle) string {
	text = strings.TrimPrefix(text, "-")
	intPart, frac, hasFrac := strings.Cut(text, ".")
	var b strings.Builder
	for i, c := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteString(st.group)
		}
		b.WriteRune(c)
	}
	if hasFrac {
		b.WriteString(st.decimal)
		b.WriteString(frac)
	}
	return b.String()
}
