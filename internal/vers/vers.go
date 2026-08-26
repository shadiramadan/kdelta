// Package vers provides version-string ordering per VersioningScheme.
// Sources fetch unordered strings; this package alone supplies ordering
// semantics.
package vers

import (
	"sort"
	"strings"

	"golang.org/x/mod/semver"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
)

// canonical normalizes a version string for comparison under the scheme.
// ok is false when the string does not parse under the scheme.
func canonical(value string, scheme kdeltav1.VersioningScheme) (string, bool) {
	v := strings.TrimSpace(value)
	switch scheme {
	case kdeltav1.VersioningScheme_VERSIONING_SCHEME_SEMVER,
		kdeltav1.VersioningScheme_VERSIONING_SCHEME_LOOSE_SEMVER,
		kdeltav1.VersioningScheme_VERSIONING_SCHEME_UNSPECIFIED:
		if !strings.HasPrefix(v, "v") {
			v = "v" + v
		}
		return v, semver.IsValid(v)
	default:
		// Tags and git revisions have no total order.
		return v, false
	}
}

// CompareOK orders two version strings under the scheme. ok is false when
// either side does not parse under the scheme (e.g. a git revision or an
// arbitrary tag, which have no total order); callers must not treat the
// returned 0 as "equal" in that case. This distinction matters for the
// planned git-tag and OCI schemes, which are not totally ordered.
func CompareOK(a, b string, scheme kdeltav1.VersioningScheme) (cmp int, ok bool) {
	ca, okA := canonical(a, scheme)
	cb, okB := canonical(b, scheme)
	if !okA || !okB {
		return 0, false
	}
	return semver.Compare(ca, cb), true
}

// Compare orders two version strings under the scheme, returning 0 when equal
// OR incomparable. Use it only where both inputs are known sortable (e.g.
// after a Sortable filter); otherwise use CompareOK to tell the two apart.
func Compare(a, b string, scheme kdeltav1.VersioningScheme) int {
	cmp, _ := CompareOK(a, b, scheme)
	return cmp
}

// Sortable reports whether the value participates in ordering under the
// scheme.
func Sortable(value string, scheme kdeltav1.VersioningScheme) bool {
	_, ok := canonical(value, scheme)
	return ok
}

// SortAscending orders versions oldest to newest under the scheme, dropping
// values the scheme cannot parse.
func SortAscending(versions []*kdeltav1.Version, scheme kdeltav1.VersioningScheme) []*kdeltav1.Version {
	sortable := make([]*kdeltav1.Version, 0, len(versions))
	for _, v := range versions {
		if Sortable(v.GetValue(), scheme) {
			sortable = append(sortable, v)
		}
	}
	sort.SliceStable(sortable, func(i, j int) bool {
		return Compare(sortable[i].GetValue(), sortable[j].GetValue(), scheme) < 0
	})
	return sortable
}
