package pii

import "fmt"

// The Dutch set. For now it is the licence plate alone; the BSN, which carries
// an eleven-test, is the next entry here.
//
// TODO: a Dutch plate run together ("GH318K") is a six- or seven-character code
// like any booking reference or voucher, so it is read only with its dashes. It
// needs a word naming a vehicle to be read without them.

// nlPlateFind reads every sidecode still on the road, with the dashes the Dutch
// always write: sidecodes 1–6 (1951–2008), and the cars' 7–11 and the vans' and
// tractors' 12–14 since. Sidecodes 1–4 are also Portugal's four eras, letter for
// letter; the Dutch loads first, and its letters decide.
var nlPlateFind = plateFinder(false,
	"LL-DD-DD", "DD-DD-LL", "DD-LL-DD", "LL-DD-LL", "LL-LL-DD", "DD-LL-LL",
	"DD-LLL-D", "D-LLL-DD", "LL-DDD-L", "L-DDD-LL", "LLL-DD-L",
	"L-DD-LLL", "D-LL-DDD", "DDD-LL-D")

// DutchPlateCheck reports whether a plate's letters are ones the RDW issues:
// consonants only, and never C or Q. It is what keeps "RS-232-C" out, and "AB-12-CD"
// in a Portuguese deployment.
//
// M, W, Y and L stay allowed although the series does not issue them freely: they
// are the prefixes of motorcycles, dealers, boats and tractors, and a rule that
// refused them would forward those in clear.
func DutchPlateCheck(value string) bool {
	return plateHasNone(value, "AEIOUCQ") && plateNoZeroNumber(value)
}

// netherlandsFakes are the Dutch stand-ins. A vowel is never issued, and no other
// country's plate reads "DD-LLL-D", so nobody's car carries it.
var netherlandsFakes = map[Category]Generator{
	CatLicPlate: {Capacity: 1000, Make: func(i int64) string {
		return fmt.Sprintf("%02d-AEU-%d", (i-1)/10, (i-1)%10)
	}},
}

// NetherlandsPatterns returns the Dutch set.
func NetherlandsPatterns() []Pattern {
	return []Pattern{
		{Find: nlPlateFind, Verify: DutchPlateCheck, Category: CatLicPlate, Label: "Kenteken (sidecodes)"},
	}
}
