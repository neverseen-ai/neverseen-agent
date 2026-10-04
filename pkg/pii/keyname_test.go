package pii

import "testing"

// The name a value is assigned to, read the way each language writes an assignment,
// and the names that look close and are not.
func TestNamedByItsKey(t *testing.T) {
	tests := []struct {
		cat    Category
		before string
		want   bool
	}{
		// Named, in the notations a fixture is written in.
		{CatDOB, `		BirthDate: "`, true},
		{CatDOB, `{"date_of_birth": "`, true},
		{CatDOB, `user.dateOfBirth = '`, true},
		{CatDOB, `dob: `, true},
		{CatDOB, `$ddn = "`, true},
		{CatPhone, `{"phone": "`, true},
		{CatPhone, `Phone:     "`, true},
		{CatPhone, `customer_phone_number='`, true},
		{CatPhone, `"tel" => '`, true},
		{CatPhone, `<telephone>`, true},
		{CatPhone, `mobile := "`, true},
		{CatPostalCode, `zip_code: `, true},
		{CatPostalCode, `postalCode: "`, true},
		{CatPostalCode, `"cp": "`, true},
		// A name after a character wider than a byte.
		{CatPhone, "«\u00a0téléphone : ", true},

		// Not named: the changelog and the byte array the relaxation exists for.
		{CatDOB, `{Version: "1.4.0", Date: "`, false},
		{CatDOB, `releasedAt = "`, false},
		{CatPhone, `var frameHeader = []byte{`, false},
		{CatPhone, `version: "`, false},
		// A word that opens a longer one is that longer word.
		{CatPhone, `telemetry = "`, false},
		{CatPhone, `"hotel": "`, false},
		{CatPhone, `cellspacing="`, false},
		{CatPostalCode, `zipfile: `, false},
		// The word is in the window, but it names the field before.
		{CatPhone, `{"phone": "x", "version": "`, false},
		// A positional value has no name of its own (the TODO in keyname.go).
		{CatPhone, `call("06 11 11 11 11", `, false},
		// A category the code relaxation does not touch is never asked.
		{CatEmail, `"phone": "`, false},
	}
	for _, tt := range tests {
		if got := NamedByItsKey(tt.cat, Placement{Before: tt.before}); got != tt.want {
			t.Errorf("NamedByItsKey(%s, %q) = %v, want %v", tt.cat, tt.before, got, tt.want)
		}
	}
}

// Every category the code relaxation drops has names that bring it back, so a
// category newly marked NoisyInCode cannot leave a named field in clear by omission.
func TestEveryCategoryRelaxedInCodeHasKeyWords(t *testing.T) {
	for _, cat := range Categories() {
		if NoisyInCode(cat) && len(keyWords[cat]) == 0 {
			t.Errorf("%s is relaxed in code and no name brings it back: add it to keyWords", cat)
		}
	}
}
