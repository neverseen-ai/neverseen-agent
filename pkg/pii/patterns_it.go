package pii

import (
	"fmt"
	"strings"
)

// The Italian set. For now it is the licence plate alone; the codice fiscale,
// which carries a check letter, is the next entry here.

// itPlateFind reads the plate since 1994: two letters, three digits, two letters.
// Printed since 1999 as "AB 123CD" — a gap after the first pair, the rest run
// together — and typed spaced, dashed or run together.
//
// France writes the same shape, and a value both could read is one span masked
// once, named by whichever loads first. What only Italy prints, "AB 123CD", is
// named by Italy.
var itPlateFind = plateFinder(false, "LL DDDLL", "LL DDD LL", "LL-DDD-LL", "LLDDDLL")

// ItalianPlateCheck reports whether a plate's letters are ones the series is
// issued from: never I, O, Q or U, which read as 1, 0, 0 and V.
func ItalianPlateCheck(value string) bool {
	return !strings.ContainsAny(strings.ToUpper(value), "IOQU")
}

// italyFakes are the Italian stand-ins. O and Q are never issued, and no other
// country's plate reads "LL DDDLL", so nobody's car carries it.
var italyFakes = map[Category]Generator{
	CatLicPlate: {Capacity: 1000, Make: func(i int64) string {
		return fmt.Sprintf("OQ %03dOQ", i-1)
	}},
}

// ItalyPatterns returns the Italian set.
func ItalyPatterns() []Pattern {
	return []Pattern{
		{Find: itPlateFind, Verify: ItalianPlateCheck, Category: CatLicPlate, Label: "Targa (1994)"},
	}
}
