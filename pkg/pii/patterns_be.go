package pii

import "fmt"

// The Belgian set. For now it is the licence plate alone; the national register
// number, which carries a mod-97 key, is the next entry here.
//
// TODO: no plate issued before 2010. "ABC-123" (1973–2008) and "123-ABC"
// (2008–10) are still on the road, and both are shapes technical text is full
// of — "SHA-256", "AES-128", "ISO-639" — so they need a word naming a vehicle,
// which the placement rule can require once they are measured against a corpus.

// bePlateFind reads the plate since 2010: a digit, three letters, three digits —
// "1-ABC-123" — printed with dashes and typed with spaces, with nothing, or in
// lowercase.
var bePlateFind = plateFinder(false, "D-LLL-DDD", "D LLL DDD", "DLLLDDD")

// BelgianPlateCheck reports whether a plate opens on a digit the series uses.
//
// The leading digit is 1 to 9: the series opened on 1, is on 2 since 2021, and 8
// and 9 were used for special series. It is never 0. Every letter is allowed: I
// and O are not issued today but were, and a letter rule written from this year's
// practice is the one that forwards next year's cars in clear.
func BelgianPlateCheck(value string) bool { return value != "" && value[0] != '0' }

// belgiumFakes are the Belgian stand-ins. The leading digit is never 0, so the
// pattern cannot read this back, and no Belgian car carries it.
var belgiumFakes = map[Category]Generator{
	CatLicPlate: {Capacity: 1000, Make: func(i int64) string {
		return fmt.Sprintf("0-AAA-%03d", i-1)
	}},
}

// BelgiumPatterns returns the Belgian set.
func BelgiumPatterns() []Pattern {
	return []Pattern{
		{Find: bePlateFind, Verify: BelgianPlateCheck, Category: CatLicPlate, Label: "Plaque / nummerplaat (2010)"},
	}
}
