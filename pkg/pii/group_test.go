package pii

import "testing"

// Every category belongs to exactly one registered group, and every group holds
// at least one category.
//
// validateCatalogue already panics on a category with no group, so this test says
// so by name rather than by the package failing to load. The other half is the
// one the validator cannot check: a group nothing points at is a heading a menu
// would draw empty, and a group added in anticipation of categories that never
// arrived is the documentation-ahead-of-the-code failure in another form.
func TestEveryCategoryIsInAGroupAndEveryGroupIsUsed(t *testing.T) {
	held := map[Group]int{}
	for _, cat := range Categories() {
		g := GroupOf(cat)
		if _, ok := groupRegistry[g]; !ok {
			t.Errorf("category %q names group %q, which is not registered", cat, g)
			continue
		}
		held[g]++
	}

	for _, g := range Groups() {
		if held[g] == 0 {
			t.Errorf("group %q holds no category: a surface listing it would draw an empty heading", g)
		}
	}

	// The two halves have to account for the whole catalogue between them, so a
	// category cannot be dropped by a grouping that looks complete.
	total := 0
	for _, g := range Groups() {
		total += len(CategoriesInGroup(g))
	}
	if want := len(Categories()); total != want {
		t.Errorf("the groups hold %d categories between them, the catalogue has %d", total, want)
	}
}

// Groups come out in display order, not alphabetical.
//
// Load order is a decision here as it is in the locale registry: the first
// entries are the ones somebody opened the menu for. Alphabetical puts Banking —
// the group nobody switches off by accident — above the personal details, which
// is the group they came for.
func TestGroupsAreInDisplayOrder(t *testing.T) {
	got := Groups()

	want := []Group{
		GroupPersonal, GroupCompany, GroupTechnical, GroupBanking,
		GroupDeclared, GroupConnection, GroupSecrets,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d groups, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("group %d is %q, want %q", i, got[i], want[i])
		}
	}
}

// A credential is never switchable, whatever group it was put in.
//
// The rule follows Secret rather than the group, which is why the connection
// string sits in a group of its own and is still locked. A menu that hid the
// credential rows would still be talking to an agent that accepted "disable
// SECRET_ANTHROPIC_KEY" from anything else on the machine.
func TestNoCredentialIsSwitchable(t *testing.T) {
	for _, cat := range Categories() {
		if IsSecret(cat) && Switchable(cat) {
			t.Errorf("credential %q is switchable: unticking it hands a live key to a provider", cat)
		}
	}

	if Switchable(CatConnStr) {
		t.Error("the connection string is switchable — its own group must not have unlocked it")
	}
	// The deployment's own declaration is the one decision somebody made
	// explicitly; a menu must not undo it.
	if Switchable(CatCustom) {
		t.Error("CUSTOM is switchable: a menu would undo the deployment's own declaration")
	}
	if !Switchable(CatEmail) {
		t.Error("an ordinary category is not switchable, so nothing can be switched at all")
	}
}

// An unregistered category is not switchable. The disabled set arrives from a
// request, so an unknown name must be refused rather than silently ignored.
func TestAnUnknownCategoryIsNotSwitchable(t *testing.T) {
	if Switchable(Category("INVENTED")) {
		t.Error("an unregistered category is switchable")
	}
}
