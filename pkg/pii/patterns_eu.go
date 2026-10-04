package pii

import (
	"fmt"
	"strings"
)

// The rest of Europe's licence plates, one small set per country.
//
// Each of these countries carries only its plate so far, and each plate is a
// signature and a rule, so they are kept in one file rather than thirty-three
// that would each hold six lines. A country that gains a national identifier
// moves to a file of its own. Six already have a file and still carry the plate
// alone: de for its 730 districts, and es, be, it, nl and pt because their plates
// came before this table.
//
// Two kinds, and often both in one country. A shape nothing else writes is read
// on its own, like the French plate (plateFinder). A shape ordinary text also takes — "ABC 123" is a course
// code, "AB 12345" a country and a postcode — is read only behind a word naming a
// vehicle (namedPlateFinder), because the word is the only evidence there is.
// Which kind a notation is follows the measurements in the research the wiki
// cites, and every "named" country's corpus holds the text that forced it. A
// plate run together is a run of letters and digits like any identifier —
// "test123", "1st2023", "B737MAX" — so outside the five countries whose plate
// shapes are their own (fr, es, gb, be, it), the run-together notation is read
// only behind a word even where the printed one is read without.
//
// Every stand-in carries a number made of zeros, which no series issues, and
// plateNoZeroNumber is in every rule that reads a shared shape: a stand-in one
// country mints must not be a plate to any other.
//
// TODO: no plate written in Cyrillic or Greek letters (Bulgaria, Ukraine,
// Greece), and no Latin letter outside ASCII either: a chunk with a character
// outside ASCII is never part of a plate. So the Croatian and Serbian cities
// spelled with Č, Ć, Ž, Š or Đ, listed below because the lists are the
// countries', are unreachable, as the German districts with an umlaut are. The
// upgrade is a signature read per rune, with every country's rule refusing the
// letters its series never uses — which is why it is not a one-line change.

// euroFake is the stand-in generator for a plate: a template whose number is all
// zeros, with the index written into its letters — every "@" is one, the last
// the fastest — so two values in one conversation get two stand-ins.
func euroFake(template string) Generator {
	slots := strings.Count(template, "@")
	if slots == 0 {
		panic(fmt.Sprintf("pii: plate stand-in template %q has no @ to carry the index", template))
	}
	capacity := int64(1)
	for range slots {
		capacity *= 26
	}
	return Generator{Capacity: capacity, Make: func(i int64) string {
		i--
		out := []byte(template)
		for p := len(out) - 1; p >= 0; p-- {
			if out[p] == '@' {
				out[p] = 'A' + byte(i%26)
				i /= 26
			}
		}
		return string(out)
	}}
}

// euroPlate is one country's plate: how it is found, what it must satisfy, what
// replaces it, and what a report calls it.
type euroPlate struct {
	find   func(string) [][]int
	verify func(string) bool
	fake   string
	label  string
}

func (p euroPlate) patterns() []Pattern {
	return []Pattern{{Find: p.find, Verify: p.verify, Category: CatLicPlate, Label: p.label}}
}

func (p euroPlate) fakes() map[Category]Generator {
	return map[Category]Generator{CatLicPlate: euroFake(p.fake)}
}

// Read on their own: shapes nothing else writes, with a rule that narrows them.

var ieDistricts = plateCodes(`C CE CN CW D DL G KE KK KY L LD LH LK LM LS MH MN MO OY RN SO T TN TS W WD WH WX WW`)

var czRegions = "ABCEHJKLMPSTUZ"

var roCounties = plateCodes(`AB AG AR B BC BH BN BR BT BV BZ CJ CL CS CT CV DB DJ GJ GL GR HD HR IF IL IS MH MM MS NT OT PH SB SJ SM SV TL TM TR VL VN VS`)

var hrCities = plateCodes(`BJ BM ČK DA DE DJ DU GS IM KA KC KR KT KŽ MA NA NG OG OS PS PU PŽ RI SB SK SL ST ŠI VK VT VU VŽ ZD ZG ŽU`)

var rsCities = plateCodes(`AL AR AC BB BG BO BP BT BČ BŽ ČA DE GL GM IN JA KA KC KG KI KL KM KŠ KO KV LB LE LO LU NP NG NI NS NV PA PB PE PI PK PN PO PP PR PT PŽ RA RU SA SC SD SJ SM SO SP ST SU SV ŠA ŠC TO TS TT UB UE UR VA VB VL VP VR VS ZA ZR ŽA ŽI`)

var bgProvinces = plateCodes(`A B BH BP BT C CA CB CH CM CO CT E EB EH H K KH M OB P PA PB PK PP T TX X Y`)

var euroPlates = map[string]euroPlate{
	// Ireland: year and half-year, county, sequence — "231-D-12345".
	"ie": {
		find: plateFinder(false, "DDD-L{1,2}-D{1,6}", "DD-L{1,2}-D{1,6}", "DDD L{1,2} D{1,6}"),
		verify: func(v string) bool {
			r := plateRuns(v)
			return len(r) == 3 && ieDistricts[r[1]] && plateNoZeroNumber(v)
		},
		fake:  "000-@@-0",
		label: "Irish registration",
	},
	// Czechia: a digit, the region letter, then a digit or a letter, and four
	// digits — "1A2 3456".
	"cz": {
		find: anyPlateFinder(
			plateFinder(false, "DLD DDDD", "DLL DDDD"),
			namedPlateFinder("DLDDDDD", "DLLDDDD")),
		verify: func(v string) bool {
			u := strings.ToUpper(v)
			return strings.IndexByte(czRegions, u[1]) >= 0 && plateNoZeroNumber(v)
		},
		fake:  "0@0 00@@",
		label: "SPZ (Czech)",
	},
	// Slovakia: since 2023 two letters, three digits, two letters — "BA-123AB" —
	// France's and Italy's shape, and before 2023 a district code in front.
	"sk": {
		find:   plateFinder(false, "LL-DDDLL", "LL DDDLL", "LL DDD LL", "LLDDDLL"),
		verify: func(v string) bool { return plateHasNone(v, "QW") && plateNoZeroNumber(v) },
		fake:   "@Q-000@@",
		label:  "EČV (Slovak)",
	},
	// Hungary: since 2022 four letters and three digits — "AA AB-123" — read on
	// its own only as printed, spaced after the first pair. Run together or dashed
	// whole it is "test123", "HTTP200" or a ticket key like "PROJ-123", and the
	// 1990–2022 three letters and three digits is a course code or an ISO number;
	// those are read only behind a word.
	"hu": {
		find: anyPlateFinder(
			plateFinder(false, "LL LL-DDD", "LL LL DDD"),
			namedPlateFinder("LLLL-DDD", "LLLLDDD", "LLL-DDD", "LLL DDD", "LLLDDD")),
		verify: plateNoZeroNumber,
		fake:   "A@ @@-000",
		label:  "Rendszám (Hungarian, 2022)",
	},
	// Romania: county, two digits, three letters — "CJ 12 ABC" — and Bucharest,
	// "B 123 ABC". The United Kingdom's shape; the county decides.
	"ro": {
		find: anyPlateFinder(
			plateFinder(false, "LL DD LLL", "LL-DD-LLL", "L DD LLL", "L DDD LLL"),
			namedPlateFinder("LLDDLLL", "LDDLLL", "LDDDLLL")),
		verify: func(v string) bool {
			r := plateRuns(v)
			return len(r) == 3 && roCounties[r[0]] && plateNoZeroNumber(v) &&
				!strings.ContainsAny(r[2][:1], "IO") && r[2] != "III" && r[2] != "OOO"
		},
		fake:  "B 00 @@@",
		label: "Număr de înmatriculare (Romanian)",
	},
	// Croatia: city, three or four digits, one or two letters — "ZG 1234-AB".
	"hr": {
		find: plateFinder(false, "LL D{3,4}-L{1,2}", "LL-D{3,4}-L{1,2}", "LL D{3,4} L{1,2}"),
		verify: func(v string) bool {
			r := plateRuns(v)
			return len(r) == 3 && hrCities[r[0]] && plateHasNone(v, "QWXY") && plateNoZeroNumber(v)
		},
		fake:  "ZG 000-@@",
		label: "Registarska oznaka (Croatian)",
	},
	// Serbia: city, three to five digits, two letters — "BG 123-AB".
	"rs": {
		find: plateFinder(false, "LL D{3,5}-LL", "LL-D{3,5}-LL", "LL D{3,5} LL"),
		verify: func(v string) bool {
			r := plateRuns(v)
			return len(r) == 3 && rsCities[r[0]] && plateNoZeroNumber(v)
		},
		fake:  "BG 000-@@",
		label: "Registarska tablica (Serbian)",
	},
	// Bulgaria: province, four digits, two letters — "CA 1234 AB" — in the letters
	// Latin and Cyrillic share.
	"bg": {
		find: anyPlateFinder(
			plateFinder(false, "L{1,2} DDDD LL", "L{1,2}-DDDD-LL"),
			namedPlateFinder("L{1,2}DDDDLL")),
		verify: func(v string) bool {
			r := plateRuns(v)
			return len(r) == 3 && bgProvinces[r[0]] && plateHasOnly(v, "ABEKMHOPCTYX") && plateNoZeroNumber(v)
		},
		fake:  "CA 0000 @@",
		label: "Регистрационен номер (Bulgarian)",
	},

	// Read only behind a word naming a vehicle: shapes ordinary text also takes.

	"at": {
		find: namedPlateFinder("L{1,2}(-| |)D{1,5}( |)L{1,2}", "L{1,2}(-| )L{1,3}( |)D{1,3}"),
		verify: func(v string) bool {
			return plateHasNone(v, "Q") && plateNoZeroNumber(v) && len(strings.Join(plateRuns(v), "")) <= 8
		},
		fake:  "@ 00000 @@",
		label: "Kfz-Kennzeichen (Austrian)",
	},
	"ch": {
		find:   namedPlateFinder("LL( |-|)D{1,6}"),
		verify: func(v string) bool { r := plateRuns(v); return len(r) == 2 && chCantons[r[0]] && plateNoLeadingZero(v) },
		fake:   "ZH 0@@@",
		label:  "Kontrollschild (Swiss)",
	},
	"li": {
		find: namedPlateFinder("LL( |-|)D{1,5}"),
		verify: func(v string) bool {
			r := plateRuns(v)
			return len(r) == 2 && r[0] == "FL" && plateNoLeadingZero(v)
		},
		fake:  "FL 0@@@",
		label: "Kontrollschild (Liechtenstein)",
	},
	"lu": {
		find:   namedPlateFinder("LL( |-|)D{3,5}", "L( |-|)DDDD"),
		verify: plateNoZeroNumber,
		fake:   "@@ 0000",
		label:  "Plaque luxembourgeoise",
	},
	"pl": {
		find: namedPlateFinder(
			"LL( |)DDDDD", "LL( |)DDDDL", "LL( |)DDDLL", "LL( |)DLDDD", "LL( |)DLLDD",
			"LLL( |)LDDD", "LLL( |)DDLL", "LLL( |)DLDD", "LLL( |)DDLD", "LLL( |)DLLD",
			"LLL( |)LLDD", "LLL( |)DDDDD", "LLL( |)DDDDL", "LLL( |)DDDLL"),
		verify: func(v string) bool { return plateHasNone(v, "Q") && plateNoZeroNumber(v) },
		fake:   "@@@ 00000",
		label:  "Tablica rejestracyjna (Polish)",
	},
	"dk": {
		find:   namedPlateFinder("LL DD DDD", "LLDDDDD", "LL DDDDD", "LL-DD-DDD"),
		verify: plateNoZeroNumber,
		fake:   "@@ 00 000",
		label:  "Nummerplade (Danish)",
	},
	"se": {
		find:   namedPlateFinder("LLL DDD", "LLLDDD", "LLL DDL", "LLLDDL", "LLL-DDD"),
		verify: func(v string) bool { return plateHasNone(v, "IQVÅÄÖ") && plateNoZeroNumber(v) },
		fake:   "@@@ 000",
		label:  "Registreringsnummer (Swedish)",
	},
	"no": {
		find:   namedPlateFinder("LL DDDDD", "LLDDDDD", "LL-DDDDD"),
		verify: func(v string) bool { return plateHasNone(v, "IOQ") && plateNoLeadingZero(v) },
		fake:   "@@ 00000",
		label:  "Kjennemerke (Norwegian)",
	},
	"fi": {
		find:   namedPlateFinder("L{2,3}-D{1,3}", "L{2,3} D{1,3}", "L{2,3}D{1,3}"),
		verify: plateNoLeadingZero,
		fake:   "@@@-0",
		label:  "Rekisterikilpi (Finnish)",
	},
	"is": {
		find:   namedPlateFinder("LL DDD", "LL LDD", "LLDDD", "LLLDD", "LL-DDD"),
		verify: plateNoZeroNumber,
		fake:   "@@ @00",
		label:  "Bílnúmer (Icelandic)",
	},
	"ee": {
		find:   namedPlateFinder("DDD LLL", "DDDLLL", "DDD-LLL"),
		verify: plateNoZeroNumber,
		fake:   "000 @@@",
		label:  "Numbrimärk (Estonian)",
	},
	"lv": {
		find:   namedPlateFinder("LL-D{1,4}", "LL D{1,4}", "LLD{1,4}"),
		verify: plateNoLeadingZero,
		fake:   "@@-0",
		label:  "Numurzīme (Latvian)",
	},
	"lt": {
		find:   namedPlateFinder("LLL DDD", "LLLDDD", "LLL-DDD"),
		verify: plateNoZeroNumber,
		fake:   "@@@ 000",
		label:  "Valstybinis numeris (Lithuanian)",
	},
	"si": {
		find:   namedPlateFinder("LL( |-)LL-DDD", "LL( |-)DD-LLL", "LL( |-)LD-DDL"),
		verify: plateNoZeroNumber,
		fake:   "LJ @@-000",
		label:  "Registrska tablica (Slovenian)",
	},
	"gr": {
		find:   namedPlateFinder("LLL-DDDD", "LLL DDDD", "LLLDDDD"),
		verify: func(v string) bool { return plateHasOnly(v, "ABEZHIKMNOPTYX") && plateNoLeadingZero(v) },
		fake:   "@@@-0000",
		label:  "Πινακίδα (Greek, in Latin letters)",
	},
	"mt": {
		find:   namedPlateFinder("LLL DDD", "LLLDDD", "LLL-DDD"),
		verify: plateNoZeroNumber,
		fake:   "@@@ 000",
		label:  "Maltese registration",
	},
	"cy": {
		find:   namedPlateFinder("LLL DDD", "LLLDDD", "LLL-DDD"),
		verify: plateNoZeroNumber,
		fake:   "@@@ 000",
		label:  "Cypriot registration",
	},
	"al": {
		find:   plateFinder(false, "LL DDD LL", "LL-DDD-LL", "LLDDDLL"),
		verify: func(v string) bool { return plateHasNone(v, "Q") && plateNoZeroNumber(v) },
		fake:   "@Q 000 @@",
		label:  "Targa (Albanian)",
	},
	"mk": {
		find:   namedPlateFinder("LL D{3,4}( |-)LL", "LLD{3,4}LL"),
		verify: plateNoZeroNumber,
		fake:   "SK 0000 @@",
		label:  "Регистарска таблица (North Macedonian)",
	},
	"ba": {
		find:   plateFinder(false, "LDD-L-DDD", "LDD L DDD"),
		verify: func(v string) bool { return plateHasOnly(v, "AEJKMOT") && plateNoZeroNumber(v) },
		fake:   "@00-@-000",
		label:  "Registarska tablica (Bosnian)",
	},
	"me": {
		find:   namedPlateFinder("LL LL DDD", "LL LL-DDD", "LLLLDDD"),
		verify: plateNoZeroNumber,
		fake:   "PG @@ 000",
		label:  "Registarska tablica (Montenegrin)",
	},
	"xk": {
		find: namedPlateFinder("DD-DDD-LL", "DD DDD LL", "DDDDDLL"),
		verify: func(v string) bool {
			r := plateRuns(v)
			return len(r) == 3 && r[0] >= "01" && r[0] <= "07" && plateNoZeroNumber(v)
		},
		fake:  "01-000-@@",
		label: "Targa (Kosovan)",
	},
	"ua": {
		find: anyPlateFinder(
			plateFinder(false, "LL DDDD LL", "LL-DDDD-LL"),
			namedPlateFinder("LLDDDDLL")),
		verify: func(v string) bool { return plateHasOnly(v, "ABEIKMHOPCTX") && plateNoZeroNumber(v) },
		fake:   "A@ 0000 @@",
		label:  "Номерний знак (Ukrainian, in Latin letters)",
	},
	"md": {
		find:   namedPlateFinder("LLL DDD", "LLLDDD", "LLL-DDD"),
		verify: plateNoZeroNumber,
		fake:   "@@@ 000",
		label:  "Număr de înmatriculare (Moldovan)",
	},
	// Behind a word: "10 AB 12" is a version and two numbers as readily as a plate
	// from Adana, and the province code is any two digits up to 81.
	"tr": {
		find: namedPlateFinder("DD L{1,3} D{2,5}", "DDL{1,3}D{2,5}"),
		verify: func(v string) bool {
			r := plateRuns(v)
			return len(r) == 3 && r[0] >= "01" && r[0] <= "81" && plateHasNone(v, "QWX") && plateNoZeroNumber(v)
		},
		fake:  "34 @@@ 00",
		label: "Plaka (Turkish)",
	},
}

var chCantons = plateCodes(`AG AI AR BE BL BS FR GE GL GR JU LU NE NW OW SG SH SO SZ TG TI UR VD VS ZG ZH`)

// euroPlateLocale builds the registry entry for one of these countries.
func euroPlateLocale(code string, priority int, sample string) Locale {
	p, ok := euroPlates[code]
	if !ok {
		panic(fmt.Sprintf("pii: no European plate for %q", code))
	}
	return Locale{
		Code: code, Priority: priority, European: true,
		Patterns: p.patterns, Sample: sample, Fakes: p.fakes(),
	}
}
