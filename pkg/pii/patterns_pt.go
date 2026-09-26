package pii

import "fmt"

// The Portuguese set. For now it is the licence plate alone; the NIF, which
// carries a check digit, is the next entry here.
//
// TODO: a plate spaced ("AT 45 RE") or run together is read nowhere yet. Spaced,
// "AB 12 34" is the head of a British National Insurance number, and a plate
// claiming it would leave the rest of that number in clear; both need a word
// naming a vehicle to be read.

// ptPlateFind reads the four eras, dashed as they are typed: "AB-12-CD" since
// 2020, "12-AB-34" before it, "12-34-AB" and "AB-12-34" before that. Every one is
// also a Dutch sidecode; a value both read is Dutch when its letters are, and
// Portuguese when they carry a vowel.
var ptPlateFind = plateFinder(false, "LL-DD-LL", "DD-LL-DD", "DD-DD-LL", "LL-DD-DD")

// portugalFakes are the Portuguese stand-ins: the pair 00 is never issued, and
// spaced, no country's pattern reads the shape at all.
var portugalFakes = map[Category]Generator{
	CatLicPlate: {Capacity: 26 * 26 * 26, Make: func(i int64) string {
		i--
		return fmt.Sprintf("A%c 00 %c%c", 'A'+byte(i/676%26), 'A'+byte(i/26%26), 'A'+byte(i%26))
	}},
}

// PortugalPatterns returns the Portuguese set. No letter rule: since 2020 every
// letter is issued, K, W and Y included.
func PortugalPatterns() []Pattern {
	return []Pattern{
		{Find: ptPlateFind, Category: CatLicPlate, Label: "Matrícula portuguesa"},
	}
}
