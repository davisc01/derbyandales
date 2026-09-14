package publish

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Planning a publish.
//
// Nothing is written until it has been shown. The club's site is a git working
// tree that somebody reviews and commits, so the worst outcome here is not a
// failed write — it is a silent one that turns up in a commit three weeks later.

// Status says what publishing a file would do.
type Status string

const (
	// StatusNew: the file does not exist yet.
	StatusNew Status = "new"
	// StatusSame: the file exists and would not change.
	StatusSame Status = "unchanged"
	// StatusChanged: the file exists and would be overwritten.
	StatusChanged Status = "changed"
	// StatusKept: the file exists and will be left alone. Used for index.md,
	// which carries a hand-written summary and a photo album link that no
	// export should ever flatten.
	StatusKept Status = "kept"
)

// File is one file a publish would write.
type File struct {
	// Path is relative to the site root, which is what a person recognises.
	Path    string
	Content []byte
	Status  Status

	// Note explains a status that is not obvious.
	Note string

	// Added and Removed are line counts, and Diff is the change itself.
	Added   int
	Removed int
	Diff    []DiffLine
}

// Writes reports whether applying the plan would touch this file.
func (f File) Writes() bool { return f.Status == StatusNew || f.Status == StatusChanged }

// DiffLine is one line of a change, marked with how it differs.
type DiffLine struct {
	// Mark is ' ' for context, '+' for added, '-' for removed.
	Mark byte
	Text string
}

// Plan is everything a publish would do.
type Plan struct {
	Root  string
	Label string
	Files []File
}

// Changes counts the files that would actually be written.
func (p Plan) Changes() int {
	n := 0
	for _, f := range p.Files {
		if f.Writes() {
			n++
		}
	}
	return n
}

// SiteRoot checks that a path looks like the club's Hugo site, so a mistyped
// setting does not scatter CSV files through somebody's home directory.
func SiteRoot(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("set the website folder in Settings first")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot open %s — check the website folder in Settings", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is a file, not the website folder", path)
	}
	// content/races is where every published result lives. Its absence means
	// this is some other folder.
	if _, err := os.Stat(filepath.Join(path, "content", "races")); err != nil {
		return fmt.Errorf("%s does not look like the website: it has no content/races folder", path)
	}
	return nil
}

// RacePath is where a race's files live. The championship has its own folder
// rather than a numbered one, which is how the site has always been laid out.
func RacePath(year, number int, championship bool) string {
	if championship {
		return filepath.Join("content", "races", fmt.Sprint(year), "championship")
	}
	return filepath.Join("content", "races", fmt.Sprint(year), fmt.Sprintf("race-%d", number))
}

// SeasonPath is where the season standings live.
func SeasonPath(year int) string {
	return filepath.Join("content", "races", fmt.Sprint(year), "season-standings")
}

// normalise strips a byte-order mark and carriage returns, so a file can be
// compared on what it says rather than on how it was saved.
//
// The club's committed files are a museum of exporters: some carry a BOM, most
// use CRLF, one has no final newline. Comparing raw bytes would report every
// one of them as entirely rewritten on the first publish, which would bury the
// one row that actually changed — and the point of the diff is that somebody
// can read it.
func normalise(b []byte) []byte {
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	if len(b) > 0 && !bytes.HasSuffix(b, []byte("\n")) {
		b = append(b, '\n')
	}
	return b
}

// stage works out what writing content to a path would do.
func stage(root, path string, content []byte) File {
	f := File{Path: path, Content: content}

	existing, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		f.Status = StatusNew
		for _, line := range splitLines(content) {
			f.Diff = append(f.Diff, DiffLine{'+', line})
			f.Added++
		}
		return f
	}

	clean := normalise(existing)
	if bytes.Equal(clean, content) {
		// Same table. If the file on disk carries a BOM it still has to be
		// rewritten: the site matches column names by string equality, and a
		// BOM lives inside the first one — which silently breaks the season
		// page's hidden and highlighted columns.
		if bytes.HasPrefix(existing, []byte{0xEF, 0xBB, 0xBF}) {
			f.Status = StatusChanged
			f.Note = "same contents, but removing a byte-order mark that breaks " +
				"the website's column matching"
			return f
		}
		f.Status = StatusSame
		return f
	}

	f.Status = StatusChanged
	f.Diff = diffLines(splitLines(clean), splitLines(content))
	for _, d := range f.Diff {
		switch d.Mark {
		case '+':
			f.Added++
		case '-':
			f.Removed++
		}
	}
	return f
}

// keep stages a file that exists and must not be overwritten.
func keep(root, path string, content []byte, note string) File {
	if _, err := os.Stat(filepath.Join(root, path)); err == nil {
		return File{Path: path, Status: StatusKept, Note: note}
	}
	f := stage(root, path, content)
	return f
}

// Apply writes the plan. Directories are created as needed.
//
// A file that has not changed is not rewritten, so its modification time stays
// put and `git status` stays honest about what happened tonight.
func Apply(p Plan) ([]string, error) {
	var written []string
	for _, f := range p.Files {
		if !f.Writes() {
			continue
		}
		full := filepath.Join(p.Root, f.Path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return written, fmt.Errorf("creating %s: %w", filepath.Dir(f.Path), err)
		}
		if err := os.WriteFile(full, f.Content, 0o644); err != nil {
			return written, fmt.Errorf("writing %s: %w", f.Path, err)
		}
		written = append(written, f.Path)
	}
	return written, nil
}

// --- diffing ----------------------------------------------------------------------

func splitLines(b []byte) []string {
	s := strings.TrimSuffix(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// contextLines is how many unchanged lines to show around a change. Enough to
// recognise which row moved, few enough that a 100-row heat file with one
// corrected time shows as a handful of lines rather than two hundred.
const contextLines = 2

// diffLines produces a compact line diff with a little context.
//
// A published file is a table in a stable order, so a real diff is worth having:
// "one time changed in heat 12" and "every row shifted because a car was
// excluded" look identical as counts and completely different as lines.
func diffLines(old, new []string) []DiffLine {
	lcs := longestCommon(old, new)

	var all []DiffLine
	i, j := 0, 0
	for _, k := range lcs {
		for i < k.a {
			all = append(all, DiffLine{'-', old[i]})
			i++
		}
		for j < k.b {
			all = append(all, DiffLine{'+', new[j]})
			j++
		}
		all = append(all, DiffLine{' ', old[i]})
		i++
		j++
	}
	for ; i < len(old); i++ {
		all = append(all, DiffLine{'-', old[i]})
	}
	for ; j < len(new); j++ {
		all = append(all, DiffLine{'+', new[j]})
	}

	return trimContext(all)
}

// trimContext drops runs of unchanged lines far from any change.
func trimContext(all []DiffLine) []DiffLine {
	keepLine := make([]bool, len(all))
	for i, d := range all {
		if d.Mark == ' ' {
			continue
		}
		lo, hi := i-contextLines, i+contextLines
		if lo < 0 {
			lo = 0
		}
		if hi >= len(all) {
			hi = len(all) - 1
		}
		for k := lo; k <= hi; k++ {
			keepLine[k] = true
		}
	}

	var out []DiffLine
	gap := false
	for i, d := range all {
		if keepLine[i] {
			if gap {
				out = append(out, DiffLine{' ', "…"})
				gap = false
			}
			out = append(out, d)
			continue
		}
		gap = true
	}
	return out
}

type pair struct{ a, b int }

// longestCommon returns the indices of a longest common subsequence.
//
// Quadratic, which is fine: the biggest file here is a hundred heat rows.
func longestCommon(a, b []string) []pair {
	n, m := len(a), len(b)
	table := make([][]int, n+1)
	for i := range table {
		table[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table[i][j] = table[i+1][j+1] + 1
			} else if table[i+1][j] >= table[i][j+1] {
				table[i][j] = table[i+1][j]
			} else {
				table[i][j] = table[i][j+1]
			}
		}
	}

	var out []pair
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, pair{i, j})
			i++
			j++
		case table[i+1][j] >= table[i][j+1]:
			i++
		default:
			j++
		}
	}
	return out
}

// --- building a plan ---------------------------------------------------------------

// RaceFiles is everything one race night publishes.
type RaceFiles struct {
	Page       Page
	Heats      []HeatRow
	Standings  []StandingRow
	Awards     []AwardRow
	TrackFt    float64
	ScaleDenom int
}

// PlanRace works out what publishing one race would write.
func PlanRace(root string, r RaceFiles) (Plan, error) {
	if err := SiteRoot(root); err != nil {
		return Plan{}, err
	}
	dir := RacePath(r.Page.Year, r.Page.Number, r.Page.Championship)

	label := r.Page.Name
	if label == "" {
		label = fmt.Sprintf("race %d", r.Page.Number)
	}
	p := Plan{Root: root, Label: label}

	heats, err := HeatsCSV(r.Heats, r.TrackFt, r.ScaleDenom)
	if err != nil {
		return p, err
	}
	standings, err := StandingsCSV(r.Standings)
	if err != nil {
		return p, err
	}

	p.Files = append(p.Files,
		keep(root, filepath.Join(dir, "index.md"), IndexMD(r.Page),
			"already written — it has the summary and the photo album link in it"),
		stage(root, filepath.Join(dir, "standings.csv"), standings),
		stage(root, filepath.Join(dir, "heats.csv"), heats),
	)

	// The championship has no awards page. Its trophy is the bracket.
	if !r.Page.Championship {
		awards, err := AwardsCSV(r.Awards)
		if err != nil {
			return p, err
		}
		p.Files = append(p.Files, stage(root, filepath.Join(dir, "awards.csv"), awards))
	}
	return p, nil
}

// SeasonFiles is everything the season standings page publishes.
type SeasonFiles struct {
	Page       SeasonPage
	Qualifiers []QualifierRow
	Wildcard   []WildcardRow
}

// PlanSeason works out what publishing the season standings would write.
func PlanSeason(root string, s SeasonFiles) (Plan, error) {
	if err := SiteRoot(root); err != nil {
		return Plan{}, err
	}
	dir := SeasonPath(s.Page.Year)
	p := Plan{Root: root, Label: fmt.Sprintf("%d season standings", s.Page.Year)}

	quals, err := QualifiersCSV(s.Qualifiers)
	if err != nil {
		return p, err
	}
	wild, err := WildcardCSV(s.Wildcard)
	if err != nil {
		return p, err
	}

	p.Files = append(p.Files,
		keep(root, filepath.Join(dir, "index.md"), SeasonIndexMD(s.Page),
			"already written — edit it on the site if the wording needs to change"),
		stage(root, filepath.Join(dir, "qualifiers.csv"), quals),
		stage(root, filepath.Join(dir, "wildcard.csv"), wild),
	)
	return p, nil
}
