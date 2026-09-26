package pii

import (
	"fmt"
	"strings"
)

// The Spanish set. For now it is the licence plate alone, in its two notations
// still on the road; the national identifiers (DNI, NIE, NIF) are the next
// entries here, and each carries a check letter.

var (
	// The national plate since 2000: four digits and three letters, printed with
	// a space and typed with a dash, with nothing, or in lowercase.
	esPlateFind = plateFinder(false, "DDDD LLL", "DDDD-LLL", "DDDDLLL")

	// The provincial plate, 1971–2000: a province, four digits, one or two
	// letters — "M-1234-AB". Still common, because Spain's fleet is old. Dashed or
	// spaced only, and uppercase only: run together or in lowercase it is any
	// reference code, and the province list is the only evidence the shape has.
	esProvincialPlateFind = plateFinder(true,
		"L-DDDD-L", "L-DDDD-LL", "LL-DDDD-L", "LL-DDDD-LL",
		"L DDDD L", "L DDDD LL", "LL DDDD L", "LL DDDD LL")
)

// spainFakes are the Spanish stand-ins. A vowel is never issued, so the national
// pattern cannot read this back, and no Spanish car carries it.
var spainFakes = map[Category]Generator{
	CatLicPlate: {Capacity: 10000, Make: func(i int64) string {
		return fmt.Sprintf("%04d AEU", i-1)
	}},
}

// SpainPatterns returns the Spanish set, the national plate before the
// provincial one: the national shape is the one issued today.
func SpainPatterns() []Pattern {
	return []Pattern{
		{Find: esPlateFind, Verify: SpanishPlateCheck, Category: CatLicPlate, Label: "Matrícula (nacional)"},
		{Find: esProvincialPlateFind, Verify: ProvincialPlateCheck, Category: CatLicPlate,
			Label: "Matrícula (provincial)"},
	}
}

// esProvinces are the codes the 1971–2000 plates open on, with the renamings
// both ways round — GE and GI for Girona, OR and OU for Ourense, PM and IB for
// the Balearics — because a car keeps the plate it was issued.
var esProvinces = func() map[string]bool {
	out := make(map[string]bool)
	for _, code := range strings.Fields(`
		A AB AL AV B BA BI BU C CA CC CE CO CR CS CU GC GE GI GR GU H HU IB J L
		LE LO LU M MA ML MU NA O OR OU P PM PO S SA SE SG SO SS T TE TF TO V VA
		VI Z ZA`) {
		out[code] = true
	}
	return out
}()

// SpanishPlateCheck reports whether a national plate's letters are ones the
// series is issued from: twenty, no vowel, no Ñ, no Q. It is the plate's strongest
// rule — what keeps "1500 EUR" and "2024 AOC" out.
func SpanishPlateCheck(value string) bool {
	for i := 0; i < len(value); i++ {
		c := value[i] | 0x20 // ASCII lowercase
		if c >= 'a' && c <= 'z' && !strings.ContainsRune("bcdfghjklmnprstvwxyz", rune(c)) {
			return false
		}
	}
	return true
}

// ProvincialPlateCheck reports whether a provincial plate opens on a province.
func ProvincialPlateCheck(value string) bool {
	code, _, _ := strings.Cut(strings.Replace(value, " ", "-", 1), "-")
	return esProvinces[code]
}

// esAmountSuffixes are the series that are also what follows a number in prose:
// the ISO 4217 currencies written in the plate's twenty letters, and the units.
// "1500 GBP" and "2400 MHZ" are the national shape exactly.
//
// Asked by the placement rule, never used to drop a value outright: GBP is a
// series Spain has issued, and a plate on it named as one ("matrícula 1500 GBP")
// is still somebody's car.
var esAmountSuffixes = func() map[string]bool {
	out := make(map[string]bool)
	for _, code := range strings.Fields(`
		BBD BDT BGN BHD BMD BND BSD BTN BWP BYN BZD CDF CHF CHW CLP CNY CZK DKK
		FJD FKP GBP GHS GMD GNF GYD HKD HNL HTG JMD JPY KGS KHR KMF KPW KRW KYD
		KZT LBP LKR LRD LSL LYD MDL MKD MMK MNT MWK MXN MXV MZN NGN NPR NZD PGK
		PHP PKR PLN PYG RSD RWF SBD SDG SGD SHP SLL SRD SSP STN SVC SYP SZL THB
		TJS TMT TTD TWD TZS VND WST XCD XDR XPD XPF XTS ZMW ZWG ZWL
		KHZ MHZ GHZ THZ KWH MWH GWH RPM BPM MPH KPH KMH PPM FPS`) {
		out[code] = true
	}
	return out
}()
