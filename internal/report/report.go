// Package report renders the REPORT.md of one repository from a discover/v1 model, and from nothing else.
//
// The report has a frontmatter, a metadata block echoing the attestation, and ten numbered sections that always
// appear, in a fixed order. A section with nothing in it says so in one line; it is never left out. Every string
// that comes from the repository is escaped (escape.go), and the same model always renders to the same bytes.
package report

import (
	"bytes"
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/saphan-ai/discover/internal/model"
)

// section is one level-two heading of the report and the function that writes what stands under it.
type section struct {
	heading string
	render  func(b *bytes.Buffer, r model.Report, rp model.Repo)
}

// sections is the contract per-repo-report-v1: the metadata block, then sections 1 to 10, in this order.
var sections = []section{
	{"Repo metadata (attestation echo)", renderMetadata},
	{"1. Top-level inventory", renderInventory},
	{"2. Tech stack inventory", renderStack},
	{"3. Project structure deep-dive", renderStructure},
	{"4. Entry points", renderEntryPoints},
	{"5. Build / run / test", renderCommands},
	{"6. Cross-repo wiring", renderWiring},
	{"7. Findings", renderFindings},
	{"8. UI strings & screens preview", renderUI},
	{"9. Open questions", renderOpenQuestions},
	{"10. Recommendations", renderRecommendations},
}

// relatedOrder is what the frontmatter names as the order behind the scan. The v1 model records no order, so a
// report never has one to name.
const relatedOrder = "none"

// Render returns the REPORT.md of r.Repos[repo].
func Render(r model.Report, repo int) ([]byte, error) {
	if repo < 0 || repo >= len(r.Repos) {
		return nil, fmt.Errorf("report: repository index %d is out of range; the report holds %d repositories", repo, len(r.Repos))
	}
	rp := r.Repos[repo]
	var b bytes.Buffer
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\n", yamlString("Discover report: "+rp.Name))
	b.WriteString("type: briefing\n")
	fmt.Fprintf(&b, "related order: %s\n", relatedOrder)
	fmt.Fprintf(&b, "version: %s\n", yamlString(r.Tool.Version))
	fmt.Fprintf(&b, "last_reviewed: %s\n", yamlString(r.Scan.Finished))
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# Discover report: %s\n", text(rp.Name))
	for _, s := range sections {
		fmt.Fprintf(&b, "\n## %s\n\n", s.heading)
		s.render(&b, r, rp)
		trimEnd(&b)
	}
	return b.Bytes(), nil
}

// modelOff is said wherever a section is written by a model and the scan ran without one.
const modelOff = "this scan used none; a later version adds `--model` to turn one on."

// absent writes the one honest-absence line of section n: what was looked for, that none was found, and the
// reasons the scan recorded for the section. extra, when set, is a further sentence on the same line.
func absent(b *bytes.Buffer, rp model.Repo, n int, lookedFor, extra string) {
	var reasons []string
	for _, a := range rp.Absent {
		if a.Section == n {
			reasons = append(reasons, text(strings.TrimRight(a.Reason, ". ")))
		}
	}
	line := "_Absent._ Looked for " + lookedFor + "; none was found."
	if len(reasons) == 0 {
		line += " The scan recorded no reason."
	} else {
		line += " Reason recorded by the scan: " + strings.Join(reasons, "; ") + "."
	}
	if extra != "" {
		line += " " + extra
	}
	b.WriteString(line + "\n")
}

// table writes a GFM table. The header is fixed text; every cell is already escaped by its caller.
func table(b *bytes.Buffer, header []string, rows [][]string) {
	b.WriteString("| " + strings.Join(header, " | ") + " |\n")
	b.WriteString(strings.Repeat("|---", len(header)) + "|\n")
	for _, row := range rows {
		b.WriteString("| " + strings.Join(row, " | ") + " |\n")
	}
}

// block writes one labelled part of a section: its table, or one line saying none was found.
func block(b *bytes.Buffer, label string, header []string, rows [][]string) {
	if len(rows) == 0 {
		fmt.Fprintf(b, "**%s**: none found.\n\n", label)
		return
	}
	fmt.Fprintf(b, "**%s**\n\n", label)
	table(b, header, rows)
	b.WriteString("\n")
}

// source prints where a claim comes from: `file:line`, `file:key`, or `file:line` and its key when both are set.
func source(s model.Source) string {
	switch {
	case s.Line > 0 && s.Key != "":
		return code(s.File+":"+strconv.Itoa(s.Line)) + " key " + code(s.Key)
	case s.Line > 0:
		return code(s.File + ":" + strconv.Itoa(s.Line))
	default:
		return code(s.File + ":" + s.Key)
	}
}

// sources prints a list of citations in the order the model gives them.
func sources(ss []model.Source) string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = source(s)
	}
	return strings.Join(out, ", ")
}

// compareSource orders sources by file, then line, then key.
func compareSource(a, b model.Source) int {
	return cmp.Or(cmp.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line), cmp.Compare(a.Key, b.Key))
}

// sorted returns a sorted copy of xs; the model it came from is not touched.
func sorted[T any](xs []T, compare func(a, b T) int) []T {
	out := slices.Clone(xs)
	slices.SortStableFunc(out, compare)
	return out
}

// word prints an enum value of the schema as words: depends_on is "depends on".
func word(s string) string {
	return text(strings.ReplaceAll(s, "_", " "))
}

// trimEnd leaves exactly one newline at the end of what was written, so every section is spaced alike.
func trimEnd(b *bytes.Buffer) {
	trimmed := bytes.TrimRight(b.Bytes(), "\n")
	b.Truncate(len(trimmed))
	b.WriteString("\n")
}
