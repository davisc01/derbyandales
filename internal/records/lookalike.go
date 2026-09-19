package records

import (
	"sort"
	"strings"
)

// Names that may be one person.
//
// The archive has "Keith Furguson" in one race and "Keith Ferguson" in the
// next. Joined loosely they would be one career; kept apart they are two, each
// with half the wins. Neither is safe to do silently — two real people can
// share a surname and nearly share a first name — so the pair is shown to a
// person, who corrects whichever spelling is wrong.

// lookalikes finds pairs of racers who share one half of their name and are a
// letter or two apart on the other.
func lookalikes(runs []Run, results []Result) [][2]string {
	names := map[string]string{} // person key -> a spelling to show
	for _, r := range runs {
		names[r.Person] = r.Driver
	}
	for _, r := range results {
		names[r.Person] = r.Driver
	}
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var out [][2]string
	for i, a := range keys {
		for _, b := range keys[i+1:] {
			if similar(a, b) {
				out = append(out, [2]string{names[a], names[b]})
			}
		}
	}
	return out
}

func similar(a, b string) bool {
	fa, la := split(a)
	fb, lb := split(b)
	switch {
	case fa == fb && la != lb:
		return nearly(la, lb)
	case la == lb && fa != fb:
		return nearly(fa, fb)
	}
	return false
}

func split(key string) (first, last string) {
	if i := strings.LastIndex(key, " "); i > 0 {
		return key[:i], key[i+1:]
	}
	return "", key
}

// nearly is an edit distance of at most two, which covers a transposed or
// doubled letter. Short names are held to one: "Jim" and "Tim" are two people.
func nearly(a, b string) bool {
	limit := 2
	if len([]rune(a)) < 5 || len([]rune(b)) < 5 {
		limit = 1
	}
	return distance(a, b) <= limit
}

func distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
