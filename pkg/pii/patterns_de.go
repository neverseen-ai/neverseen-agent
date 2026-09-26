package pii

import (
	"fmt"
	"strings"
)

// The German set. For now it is the licence plate alone.
//
// A German plate is a district of one to three letters, one or two letters, and
// one to four digits — "M-AB 1234". That shape is ordinary text: "DIN EN 1090" is
// a plate from Dinslaken, "EU A 1" one from Euskirchen. So it is read only behind
// a word naming a vehicle, and the district must be one of the codes the Federal
// Motor Transport Authority lists; the two together are what a plate has and a
// standard's number does not.
//
// TODO: a plate standing alone — a CSV cell under a "Kennzeichen" header — goes
// out in clear, for the reason vehicleWords' TODO gives.

// dePlateFind reads the shape behind a word naming a vehicle, written with the
// DIN dash, with spaces, with both, or run together, with the E (electric) or H
// (historic) suffix on or off.
//
// The district may carry an umlaut (FÜ, MÜR), and a chunk with a character
// outside ASCII is never part of a plate, so those districts are not read yet.
// TODO: read Ä, Ö and Ü in a German district.
//
// A signature reads every letter as L, so the suffix is one more letter here and
// GermanPlateCheck is what says it must be E or H.
var dePlateFind = namedPlateFinder(expandSignatures(
	"L{1,3}(-| |)L{1,2}( |-|)D{1,4}( |)(L|)",
)...)

// deDistricts are the distinguishing signs the Kraftfahrt-Bundesamt lists for
// civil registration, the codes reissued since 2012 included. X and Y are left
// out: armed forces and NATO plates carry digits only.
var deDistricts = plateCodes(`
		A AA AB ABG ABI AC AE AH AIB AIC AK ALF ALZ AM AN ANA ANG ANK AP APD ARN ART
		AS ASL ASZ AT AU AUR AW AZ AZE AÖ B BA BAD BAR BB BBG BBL BC BCH BD BE BED
		BEL BER BF BGD BGL BH BI BID BIN BIR BIT BIW BK BKS BL BLB BLK BM BN BNA BO
		BOG BOH BOR BOT BP BR BRA BRB BRG BRK BRL BRV BS BSB BSK BT BTF BUL BW BWL
		BYL BZ BÖ BÜD BÜR BÜS BÜZ C CA CAS CB CE CHA CLP CLZ CO COC COE CR CUX CW D
		DA DAH DAN DAU DBR DD DE DEG DEL DGF DH DI DIL DIN DIZ DKB DL DLG DM DN DO
		DON DS DT DU DUD DW DZ DÜW E EA EB EBE EBN EBS ECK ED EE EF EG EH EI EIC EIL
		EIN EIS EL EM EMD EMS EN ER ERB ERH ERK ERZ ES ESB ESW EU EW F FB FD FDB FDS
		FEU FF FFB FG FI FKB FL FLÖ FN FO FOR FR FRG FRI FRW FS FT FTL FW FZ FÜ FÜS
		G GA GAN GAP GC GD GDB GE GEL GEO GER GF GG GHA GHC GI GK GL GLA GM GMN GN
		GNT GOA GOH GP GR GRA GRH GRI GRM GRZ GS GT GTH GUB GUN GV GVM GW GZ GÖ GÜ H
		HA HAB HAL HAM HAS HB HBN HBS HC HCH HD HDH HDL HE HEB HEF HEI HEL HER HET
		HF HG HGN HGW HH HHM HI HIG HIP HK HL HM HMÜ HN HO HOG HOH HOL HOM HOR HOT
		HP HR HRO HS HSK HST HU HV HVL HWI HX HY HZ HÖS IGB IK IL ILL IN IZ J JB JE
		JL JÜL K KA KB KC KE KEH KEL KEM KF KG KH KI KIB KK KL KLE KLZ KM KN KO KR
		KRU KS KT KU KUS KW KY KYF KÖN KÖT KÖZ KÜN L LA LAN LAU LB LBS LBZ LC LD LDK
		LDS LE LEO LER LEV LF LG LH LI LIB LIF LIP LK LL LM LN LOS LP LR LRO LSA LSN
		LSZ LU LUK LUP LWL LÖ LÖB LÜN M MA MAB MAI MAK MAL MB MC MD ME MED MEG MEI
		MEK MEL MER MET MG MGH MGN MH MHL MI MIL MK MKK ML MM MN MO MOD MOL MON MOS
		MQ MR MS MSE MSH MSP MST MTK MTL MU MUC MVL MW MY MYK MZ MZG MÜ MÜB MÜL MÜR
		N NAB NAI NAU NB ND NDH NE NEA NEB NEC NEN NES NEU NEW NF NH NI NK NL NM NMB
		NMS NOH NOL NOM NOR NP NR NRW NT NU NVP NW NWM NY NZ NÖ OA OAL OB OBB OBG OC
		OCH OD OE OF OG OH OHA OHV OHZ OK OL OP OPR OS OSL OTW OVI OVL OZ P PA PAF
		PAN PAR PB PCH PE PEG PF PI PIR PL PLÖ PM PN PR PRÜ PS PW PZ QFT QLB R RA RC
		RD RDG RE REG REH REI RG RH RI RID RIE RL RM RN RO ROD ROF ROK ROL ROS ROT
		ROW RP RPL RS RSL RT RU RV RW RZ RÜD RÜG S SAB SAD SAL SAN SAW SB SBG SBK SC
		SCZ SDH SDL SDT SE SEB SEE SEF SEL SFB SFT SG SGH SH SHA SHG SHK SHL SI SIG
		SIM SK SL SLE SLF SLG SLK SLN SLS SLZ SLÜ SM SMÜ SN SO SOB SOG SOK SON SP
		SPB SPN SR SRB SRO ST STA STB STD STE STL STO SU SUL SW SWA SY SZ SZB SÄK
		SÖM SÜW TBB TDO TE TET TF TG THL THW TIR TO TP TR TS TT TUT TÖL TÜ UE UEM
		UFF UH UL UM UN USI V VAI VB VEC VER VG VIB VIE VIT VK VOH VR VS W WA WAF
		WAK WAN WAR WAT WB WBS WDA WE WEL WEN WER WES WF WG WHV WI WIL WIS WIT WIZ
		WK WL WLG WM WMS WN WND WO WOB WOH WOL WOR WOS WR WRN WS WSF WST WSW WT WTL
		WTM WUG WUN WUR WW WZ WZL WÜ WÜM Z ZE ZEL ZI ZIG ZP ZR ZS ZW ZZ ÖHR ÜB`)

// GermanPlateCheck reports whether a plate is one a German authority could
// issue: a listed district, one or two letters, a number of one to four digits
// that does not open on 0, eight characters at most, and an E or H suffix only
// after the number.
//
// A run-together plate has no separator to say where the district ends, so every
// split is tried: "MAB1234" is M-AB or MA-B, and either is a plate if its
// district is listed.
func GermanPlateCheck(value string) bool {
	runs := plateRuns(value)
	if n := len(runs); n > 0 && (runs[n-1] == "E" || runs[n-1] == "H") && n >= 3 {
		runs = runs[:n-1]
	}
	if len(runs) < 2 || len(runs) > 3 {
		return false
	}
	digits := runs[len(runs)-1]
	if digits[0] < '1' || digits[0] > '9' || len(digits) > 4 {
		return false
	}
	letters := strings.Join(runs[:len(runs)-1], "")
	if len(letters)+len(digits) > 8 || !plateHasOnly(letters, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		return false
	}
	if len(runs) == 3 {
		// The separator said where the district ends.
		return deDistricts[runs[0]] && len(runs[1]) <= 2
	}
	for n := 1; n <= 3 && n < len(letters); n++ {
		if deDistricts[letters[:n]] && len(letters)-n <= 2 {
			return true
		}
	}
	return false
}

// germanyFakes are the German stand-ins. The number 0 is never issued, so the
// pattern cannot read this back and nobody's car carries it.
var germanyFakes = map[Category]Generator{
	CatLicPlate: {Capacity: 26 * 26 * 26, Make: func(i int64) string {
		i--
		return fmt.Sprintf("M-%c%c 0%c", 'A'+byte(i/676%26), 'A'+byte(i/26%26), 'A'+byte(i%26))
	}},
}

// GermanyPatterns returns the German set.
func GermanyPatterns() []Pattern {
	return []Pattern{
		{Find: dePlateFind, Verify: GermanPlateCheck, Category: CatLicPlate, Label: "Kfz-Kennzeichen"},
	}
}
