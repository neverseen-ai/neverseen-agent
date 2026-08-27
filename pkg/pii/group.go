package pii

import "sort"

// A Group is the family a category belongs to for somebody reading a list of
// them: "Personal details" rather than EMAIL, PHONE, ADDRESS, POSTAL_CODE.
//
// It exists because the catalogue is forty categories and no menu, page or report
// can put forty switches in front of a person. Grouping is therefore a fact about
// how a category is *found*, and it is deliberately not the same fact as
// CategoryInfo.Secret, which decides what may be switched at all: connection
// strings are a group of their own because that is what an operator recognises,
// and they are still locked because they are credentials. Merging the two would
// mean either hiding the connection string among twenty API keys or letting a
// menu send a live database password to a provider.
type Group string

const (
	// GroupPersonal is what people reach for: the values a data subject would
	// recognise as theirs.
	GroupPersonal Group = "personal"

	// GroupBanking is separate from the personal details because all three of its
	// categories carry a checksum, so none of them is a false positive somebody
	// switches off in irritation. A deployment with this group off has chosen it.
	GroupBanking Group = "banking"

	// GroupCompany is the group with the strongest case for being switched off: a
	// company registration number is public information, and nine bare digits
	// under a Luhn key is also every internal fleet id in the building.
	GroupCompany Group = "company"

	// GroupTechnical is the other strong case. Somebody debugging a network or a
	// database needs these in the prompt, and neither is personal data in the
	// sense the rest of the catalogue means it.
	GroupTechnical Group = "technical"

	// GroupDeclared holds what a deployment declared sensitive itself. It is the
	// only category with no regex in the catalogue, and the only one somebody
	// authored on purpose — which is the argument for never letting a menu switch
	// it off.
	GroupDeclared Group = "declared"

	// GroupConnection is a group of one, and it earns it: "postgres://admin:pw@db"
	// is what a person looks for, and it is the value that exposed the overlap
	// bug — several ordinary categories score above it, so it once resolved to an
	// EMAIL match over the password.
	GroupConnection Group = "connection"

	// GroupSecrets is the twenty API keys and tokens.
	GroupSecrets Group = "secrets"
)

// GroupInfo is what a surface needs to show a group.
type GroupInfo struct {
	// Label is the name a person reads.
	Label string

	// Order is where the group sits in a list. Load order rather than
	// alphabetical, because the first entries are the ones somebody opened the
	// menu for: an alphabetical list puts Banking — the group nobody switches off
	// by accident — above the personal details, which is the whole point of
	// opening it.
	Order int
}

// groupRegistry is every group. A category names one of these and nothing else;
// checkCatalogue rejects a category naming a group that is not here, so this is
// the one place a group is declared.
var groupRegistry = map[Group]GroupInfo{
	GroupPersonal:   {Label: "Personal details", Order: 10},
	GroupCompany:    {Label: "Company identifiers", Order: 20},
	GroupTechnical:  {Label: "Technical identifiers", Order: 30},
	GroupBanking:    {Label: "Banking", Order: 40},
	GroupDeclared:   {Label: "Declared by this deployment", Order: 50},
	GroupConnection: {Label: "Connection strings", Order: 60},
	GroupSecrets:    {Label: "Secrets and keys", Order: 70},
}

// GroupLabel returns the name a person reads, or the code itself for a group that
// is not registered — which checkCatalogue has already ruled out.
func GroupLabel(g Group) string {
	if info, ok := groupRegistry[g]; ok {
		return info.Label
	}
	return string(g)
}

// Groups lists every group in display order.
func Groups() []Group {
	out := make([]Group, 0, len(groupRegistry))
	for g := range groupRegistry {
		out = append(out, g)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return groupRegistry[out[i]].Order < groupRegistry[out[j]].Order
	})
	return out
}

// CategoriesInGroup lists the categories of one group, sorted, so a surface
// listing them twice lists them the same way.
func CategoriesInGroup(g Group) []Category {
	var out []Category
	for cat, info := range categoryRegistry {
		if info.Group == g {
			out = append(out, cat)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// GroupOf returns the group a category belongs to.
func GroupOf(cat Category) Group { return categoryRegistry[cat].Group }

// Switchable reports whether a category may be switched off at all.
//
// It follows Secret and not the group, and it is the server's rule rather than a
// surface's: a menu that hid the credential entries would still be talking to an
// agent that accepted "disable SECRET_ANTHROPIC_KEY" from anything else on the
// machine. A credential in clear is a live key handed to a provider, which is the
// one outcome no configuration should be able to produce.
func Switchable(cat Category) bool {
	info, ok := categoryRegistry[cat]
	if !ok {
		return false
	}
	// CUSTOM is the deployment's own declaration rather than a shape the
	// catalogue guessed at, so switching it off from a menu would undo the one
	// decision somebody made explicitly.
	return !info.Secret && cat != CatCustom
}
