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

// The section numbers of the contract, as the model's Absent entries name them.
const (
	secInventory = iota + 1
	secStack
	secStructure
	secEntryPoints
	secCommands
	secWiring
	secFindings
	secUI
	secOpenQuestions
	secRecommendations
)

// maxTodo is the cap the schema puts on TODO/FIXME findings; the report prints it beside the count.
const maxTodo = 30

func renderMetadata(b *bytes.Buffer, r model.Report, rp model.Repo) {
	a := rp.Attestation
	rows := [][]string{
		{"Tool", text(r.Tool.Name) + " " + code(r.Tool.Version) + ", commit " + code(r.Tool.Commit)},
		{"Scan", "started " + code(r.Scan.Started) + ", finished " + code(r.Scan.Finished)},
		{"Root", code(a.Root)},
		{"Mode", text(a.Mode)},
	}
	if a.Git != nil {
		rows = append(rows, gitRows(*a.Git)...)
	} else {
		rows = append(rows, []string{"Git", "none: a plain directory, so no branch and no head"})
	}
	rows = append(rows,
		[]string{"Tree", treeCounts(a.Tree)},
		[]string{"Tree digest", code(a.Tree.Digest)},
	)
	if a.Untracked != nil {
		rows = append(rows, []string{"Untracked", treeCounts(*a.Untracked) + "; digest " + code(a.Untracked.Digest)})
	}
	p := a.ReadOnlyProof
	rows = append(rows,
		[]string{"Read-only proof", text(p.Status) + ": before " + code(p.Before) + ", after " + code(p.After)},
		[]string{"Read-only proof, whole scan", allEqual(r.Scan.ReadOnlyProofAllEqual)},
		[]string{"Read manifest", fmt.Sprintf("%d files, %d bytes read", a.ReadManifest.FilesRead, a.ReadManifest.BytesRead)},
	)
	table(b, []string{"Field", "Value"}, rows)
}

func gitRows(g model.GitState) [][]string {
	branch, head := "none", "none"
	if g.Branch != "" {
		branch = code(g.Branch)
	}
	if g.Detached {
		branch += " (detached)"
	}
	if g.Head != "" {
		head = code(g.Head)
	}
	state := "clean"
	if !g.Clean {
		paths := sorted(g.DirtyPaths, strings.Compare)
		for i, p := range paths {
			paths[i] = code(p)
		}
		state = fmt.Sprintf("dirty, %d path(s): %s", len(paths), strings.Join(paths, ", "))
	}
	return [][]string{{"Branch", branch}, {"Head", head}, {"Working tree", state}}
}

func treeCounts(t model.Tree) string {
	return fmt.Sprintf("%d files, %d bytes, %d links, %d submodules, %d unreadable", t.Files, t.Bytes, t.Links, t.Submodules, t.Unreadable)
}

func allEqual(ok bool) string {
	if ok {
		return "every repository of the scan EQUAL"
	}
	return "NOT every repository of the scan EQUAL"
}

func renderInventory(b *bytes.Buffer, _ model.Report, rp model.Repo) {
	inv := rp.Inventory
	if len(inv.TopLevel) == 0 && len(inv.Languages) == 0 {
		absent(b, rp, secInventory, "entries at the top level of the repository and files by language", "")
		return
	}
	fmt.Fprintf(b, "%d files, %d bytes in total.\n\n", inv.FilesTotal, inv.BytesTotal)
	var top [][]string
	for _, e := range sorted(inv.TopLevel, func(x, y model.TopLevelEntry) int { return cmp.Compare(x.Name, y.Name) }) {
		top = append(top, []string{code(e.Name), text(e.Kind)})
	}
	block(b, "Top level", []string{"Name", "Kind"}, top)
	var langs [][]string
	byFiles := func(x, y model.LanguageShare) int {
		return cmp.Or(cmp.Compare(y.Files, x.Files), cmp.Compare(x.Name, y.Name))
	}
	for _, l := range sorted(inv.Languages, byFiles) {
		langs = append(langs, []string{text(l.Name), strconv.Itoa(l.Files)})
	}
	block(b, "Languages", []string{"Language", "Files"}, langs)
}

// version prints a pinned version as code, and a null one as its note, which the schema fixes as "not pinned".
func version(row model.StackRow) string {
	if row.Version != nil {
		return code(*row.Version)
	}
	if row.VersionNote != "" {
		return text(row.VersionNote)
	}
	return "not pinned"
}

func renderStack(b *bytes.Buffer, _ model.Report, rp model.Repo) {
	if len(rp.Stack) == 0 {
		absent(b, rp, secStack, "manifests and build files that name a technology and its version", "")
		return
	}
	byLayer := func(x, y model.StackRow) int {
		return cmp.Or(cmp.Compare(x.Layer, y.Layer), cmp.Compare(x.Technology, y.Technology), compareSource(x.Source, y.Source))
	}
	var rows [][]string
	for _, s := range sorted(rp.Stack, byLayer) {
		rows = append(rows, []string{text(s.Layer), text(s.Technology), version(s), source(s.Source)})
	}
	table(b, []string{"Layer", "Technology", "Version", "Source file:key"}, rows)
}

func observations(obs []model.Observation) [][]string {
	var rows [][]string
	for _, o := range obs {
		rows = append(rows, []string{text(o.Text), sources(o.Examples)})
	}
	return rows
}

func renderStructure(b *bytes.Buffer, _ model.Report, rp model.Repo) {
	st, n := rp.Structure, rp.Narrative
	if len(st.Patterns)+len(st.Naming)+len(st.Architecture)+len(n.Claims) == 0 {
		extra := ""
		if n.Model == "" {
			extra = "Narrative prose is written by a model, and " + modelOff
		}
		absent(b, rp, secStructure, "patterns, naming and architecture with file:line examples", extra)
		return
	}
	header := []string{"Observation", "Examples"}
	block(b, "Patterns", header, observations(st.Patterns))
	block(b, "Naming", header, observations(st.Naming))
	block(b, "Architecture", header, observations(st.Architecture))
	switch {
	case n.Model == "":
		b.WriteString("**Narrative**: written by a model, and " + modelOff + "\n")
	case len(n.Claims) == 0:
		fmt.Fprintf(b, "**Narrative** (model %s): the model made no claim.\n", text(n.Model))
	default:
		var rows [][]string
		for _, c := range n.Claims {
			rows = append(rows, []string{text(c.Text), sources(c.Citations)})
		}
		block(b, "Narrative, written by model "+text(n.Model), []string{"Claim", "Citations"}, rows)
	}
}

func renderEntryPoints(b *bytes.Buffer, _ model.Report, rp model.Repo) {
	if len(rp.EntryPoints) == 0 {
		absent(b, rp, secEntryPoints, "main packages, command-line tools, servers, workers, libraries and scripts", "")
		return
	}
	byKind := func(x, y model.EntryPoint) int {
		return cmp.Or(cmp.Compare(x.Kind, y.Kind), cmp.Compare(x.Name, y.Name), compareSource(x.Source, y.Source))
	}
	var rows [][]string
	for _, e := range sorted(rp.EntryPoints, byKind) {
		rows = append(rows, []string{text(e.Kind), code(e.Name), source(e.Source)})
	}
	table(b, []string{"Kind", "Name", "Source"}, rows)
}

// commandOrder is the order of command kinds in section 5: build, run, test, lint.
var commandOrder = []string{"build", "run", "test", "lint"}

func renderCommands(b *bytes.Buffer, _ model.Report, rp model.Repo) {
	if len(rp.Commands)+len(rp.CI)+len(rp.Containers) == 0 {
		absent(b, rp, secCommands, "build, run, test and lint commands, CI configurations and container files", "")
		return
	}
	b.WriteString("Commands are cited from the file that defines them; none was executed.\n\n")
	byKind := func(x, y model.Command) int {
		return cmp.Or(cmp.Compare(slices.Index(commandOrder, x.Kind), slices.Index(commandOrder, y.Kind)), compareSource(x.Source, y.Source))
	}
	var cmds [][]string
	for _, c := range sorted(rp.Commands, byKind) {
		cmds = append(cmds, []string{text(c.Kind), code(c.Text), source(c.Source)})
	}
	block(b, "Commands", []string{"Kind", "Command", "Source"}, cmds)
	var ci [][]string
	for _, c := range sorted(rp.CI, func(x, y model.CI) int { return compareSource(x.Source, y.Source) }) {
		ci = append(ci, []string{text(c.System), source(c.Source)})
	}
	block(b, "CI", []string{"System", "Source"}, ci)
	var ctr [][]string
	for _, c := range sorted(rp.Containers, func(x, y model.Container) int { return compareSource(x.Source, y.Source) }) {
		image := "none named"
		if c.Image != "" {
			image = code(c.Image)
		}
		ctr = append(ctr, []string{text(c.Kind), image, source(c.Source)})
	}
	block(b, "Containers", []string{"Kind", "Image", "Source"}, ctr)
}

func renderWiring(b *bytes.Buffer, _ model.Report, rp model.Repo) {
	if len(rp.Wiring) == 0 {
		absent(b, rp, secWiring, "dependencies on, or by, other systems, each with its file:line", "")
		return
	}
	byTarget := func(x, y model.Wiring) int {
		return cmp.Or(cmp.Compare(x.Target, y.Target), cmp.Compare(x.Relation, y.Relation), compareSource(x.Source, y.Source))
	}
	var rows [][]string
	for _, w := range sorted(rp.Wiring, byTarget) {
		rows = append(rows, []string{code(w.Target), word(w.Relation), source(w.Source)})
	}
	table(b, []string{"Target", "Relation", "Source"}, rows)
}
