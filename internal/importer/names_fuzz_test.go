package importer

import (
	"fmt"
	"strings"
	"testing"
)

// FuzzUniqueName mirrors scope.prepare: the collision suffixes of one name stay valid and distinct, so the loop that
// appends them always terminates with a valid object name. A suffix may give back the name itself when it already
// ends in that suffix; prepare then moves on to the next one.
func FuzzUniqueName(f *testing.F) {
	for _, seed := range []string{
		"", "team..read", "team.read", "Team.Read", ".-a-.", "a.-b-.c", "équipe.lire", "日本.ü", "x/Y_z",
		strings.Repeat("a", 63), strings.Repeat("A", 63), strings.Repeat("a", 300), strings.Repeat("a.", 150),
		strings.Repeat("a", 62) + "-" + strings.Repeat("b", 300), strings.Repeat(strings.Repeat("x", 63)+".", 5),
	} {
		f.Add("tenant/team", seed, uint16(3))
	}
	f.Fuzz(func(t *testing.T, ns, part string, n uint16) {
		name := objectName(ns, part)
		if again := objectName(ns, part); again != name {
			t.Fatalf("objectName(%q, %q) is not deterministic: %q != %q", ns, part, name, again)
		}
		assertValidName(t, name)
		seen := map[string]bool{}
		indices := []int{1_000_000 + int(n)}
		for i := 2; i < 12+int(n%100); i++ {
			indices = append(indices, i)
		}
		for _, i := range indices {
			unique := withSuffix(name, fmt.Sprintf("-%d", i))
			assertValidName(t, unique)
			if seen[unique] {
				t.Fatalf("withSuffix(%q, -%d) = %q repeats an earlier suffix", name, i, unique)
			}
			seen[unique] = true
		}
	})
}
