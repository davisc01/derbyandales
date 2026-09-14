package publish

import (
	"fmt"
	"strings"
	"time"
)

// The Hugo page that renders the CSVs.
//
// index.md is written once and never again. It carries a hand-written summary
// and, after the night, a link to the photo album — neither of which any export
// knows about. Overwriting it to "keep it in sync" would quietly delete the
// only part of the page a person wrote.

// Page describes a race page to be created.
type Page struct {
	Year   int
	Number int
	Name   string
	Venue  string
	Date   time.Time

	// Championship pages have no awards section and a different tag.
	Championship bool
}

// IndexMD renders a new page in the club's house style, matching the site's own
// archetype so a generated page and a hand-made one look the same.
func IndexMD(p Page) []byte {
	title := p.Name
	if title == "" {
		title = fmt.Sprintf("Race %d", p.Number)
	}
	tag := "race"
	if p.Championship {
		tag = "championship"
		if p.Name == "" {
			title = fmt.Sprintf("%d Championship", p.Year)
		}
	}

	summary := title
	if p.Venue != "" {
		summary = title + " at " + p.Venue
	}

	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %q\n", title)
	fmt.Fprintf(&b, "date: %s\n", p.Date.Format("2006-01-02"))
	fmt.Fprintf(&b, "venue: %q\n", p.Venue)
	fmt.Fprintf(&b, "summary: %q\n", summary)
	fmt.Fprintf(&b, "tags: [%q, %q]\n", tag, fmt.Sprint(p.Year))
	b.WriteString("draft: false\n")
	b.WriteString("---\n\n")

	// The album link is left empty on purpose: the photos do not exist yet on
	// the night the page is written.
	b.WriteString("{{< photo-album url=\"\" >}}\n\n")

	if !p.Championship {
		b.WriteString("## Awards\n\n")
		b.WriteString("{{< csv-table file=\"awards.csv\" >}}\n\n")
	}
	b.WriteString("## Standings\n\n")
	b.WriteString("{{< csv-table file=\"standings.csv\" >}}\n\n")
	b.WriteString("## Heat Results\n\n")
	b.WriteString("{{< csv-table file=\"heats.csv\" download=\"true\" >}}\n")

	return []byte(b.String())
}

// SeasonPage describes the season standings page.
type SeasonPage struct {
	Year           int
	Races          int
	AutoQualPlaces int
	WildcardSpots  int
	MaxEntries     int
}

// SeasonIndexMD renders the season standings page, including the club's own
// explanation of how the seeding works.
//
// The seed ranges in that explanation are computed rather than written out, so
// a season with a different number of races describes itself correctly instead
// of describing the 2026 one.
func SeasonIndexMD(p SeasonPage) []byte {
	qualifiers := p.Races * p.AutoQualPlaces

	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %q\n", fmt.Sprintf("%d Season Standings", p.Year))
	fmt.Fprintf(&b, "date: %d-01-01\n", p.Year)
	fmt.Fprintf(&b, "summary: %q\n", fmt.Sprintf("%d season standings and wildcard points", p.Year))
	fmt.Fprintf(&b, "tags: [%q, %q]\n", "standings", fmt.Sprint(p.Year))
	b.WriteString("showHero: false\n")
	b.WriteString("---\n\n")

	b.WriteString("## Auto-Qualifiers\n\n")
	fmt.Fprintf(&b, "Drivers can have a maximum of %d entries into the championship. "+
		"If a driver earns more than %d auto-qualifier spots, the extra spots are "+
		"awarded to the next driver in that race.\n\n", p.MaxEntries, p.MaxEntries)

	b.WriteString("Seedings for the championship are as follows:\n")
	fmt.Fprintf(&b, "- Seeds 1-%d are for individual race winners, sorted by times.\n", p.Races)
	fmt.Fprintf(&b, "- Seeds %d-%d are the remaining auto-qualifiers, sorted by times.\n",
		p.Races+1, qualifiers)
	fmt.Fprintf(&b, "- Seeds %d-%d are the wildcard winners, sorted by points.\n\n",
		qualifiers+1, qualifiers+p.WildcardSpots)

	b.WriteString("{{< csv-table file=\"qualifiers.csv\" highlight-column=\"Over Limit\" " +
		"highlight-value=\"YES\" hide-columns=\"Over Limit\" >}}\n\n")
	b.WriteString("## Wildcard Chase\n\n")
	// The highlight marks the racers currently holding a wildcard place, so it
	// follows the setting rather than being fixed at six.
	fmt.Fprintf(&b, "{{< csv-table file=\"wildcard.csv\" highlight-rows=%q >}}\n",
		fmt.Sprint(p.WildcardSpots))

	return []byte(b.String())
}
