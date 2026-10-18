// SPDX-License-Identifier: AGPL-3.0-only

package money

import "strings"

// Currency is an ISO 4217 currency.
type Currency struct {
	Code       string
	MinorUnits int
	Symbol     string // "" when the code is printed instead
}

// iso4217 lists active codes with 2 minor units; exceptions are in minorUnits.
const iso4217 = "AED AFN ALL AMD AOA ARS AUD AWG AZN BAM BBD BDT BGN BHD BIF BMD BND BOB BOV BRL " +
	"BSD BTN BWP BYN BZD CAD CDF CHE CHF CHW CLF CLP CNY COP COU CRC CUP CVE CZK DJF DKK DOP " +
	"DZD EGP ERN ETB EUR FJD FKP GBP GEL GHS GIP GMD GNF GTQ GYD HKD HNL HTG HUF IDR ILS INR " +
	"IQD IRR ISK JMD JOD JPY KES KGS KHR KMF KPW KRW KWD KYD KZT LAK LBP LKR LRD LSL LYD MAD " +
	"MDL MGA MKD MMK MNT MOP MRU MUR MVR MWK MXN MXV MYR MZN NAD NGN NIO NOK NPR NZD OMR PAB " +
	"PEN PGK PHP PKR PLN PYG QAR RON RSD RUB RWF SAR SBD SCR SDG SEK SGD SHP SLE SOS SRD SSP " +
	"STN SVC SYP SZL THB TJS TMT TND TOP TRY TTD TWD TZS UAH UGX USD USN UYI UYU UYW UZS VED " +
	"VES VND VUV WST XAF XCD XCG XOF XPF YER ZAR ZMW ZWG"

var minorUnits = map[string]int{
	"BIF": 0, "CLP": 0, "DJF": 0, "GNF": 0, "ISK": 0, "JPY": 0, "KMF": 0, "KRW": 0, "PYG": 0,
	"RWF": 0, "UGX": 0, "UYI": 0, "VND": 0, "VUV": 0, "XAF": 0, "XOF": 0, "XPF": 0,
	"BHD": 3, "IQD": 3, "JOD": 3, "KWD": 3, "LYD": 3, "OMR": 3, "TND": 3,
	"CLF": 4, "UYW": 4,
}

var symbols = map[string]string{"EUR": "€", "USD": "$", "GBP": "£", "JPY": "¥"}

var currencies = func() map[string]Currency {
	m := map[string]Currency{}
	for _, code := range strings.Fields(iso4217) {
		mu, ok := minorUnits[code]
		if !ok {
			mu = 2
		}
		m[code] = Currency{Code: code, MinorUnits: mu, Symbol: symbols[code]}
	}
	return m
}()

// LookupCurrency returns the currency for an upper-case ISO 4217 code.
func LookupCurrency(code string) (Currency, bool) {
	c, ok := currencies[code]
	return c, ok
}
