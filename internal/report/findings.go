package report

import (
	"bytes"
	"cmp"
	"fmt"
	"strconv"
	"strings"

	"github.com/saphan-ai/discover/internal/model"
)

// findingGroups is the order of section 7's parts, and the finding kind each one lists.
var findingGroups = []struct{ kind, label string }{
	{"hot_spot", "Hot spots"},
	{"anti_pattern", "Anti-patterns"},
	{"secret", "Secrets by location"},
	{"dead_code_candidate", "Dead-code candidates"},
	{"todo", "TODO / FIXME"},
	{"prompt_injection", "Prompt-injection findings"},
}

// secretLocation prints where a secret was found: its file and line, and nothing else. A secret found by key alone
// prints its file and says the line was not recorded; the key is not printed, as the contract allows file, line
// and type only.
func secretLocation(s model.Source) string {
	if s.Line > 0 {
		return code(s.File + ":" + strconv.Itoa(s.Line))
	}
	return code(s.File) + ", line not recorded"
}

func renderFindings(b *bytes.Buffer, _ model.Report, rp model.Repo) {
	if len(rp.Findings) == 0 {
		absent(b, rp, secFindings, "hot spots, anti-patterns, secrets by location, dead-code candidates, TODO/FIXME and prompt-injection text", "")
		return
	}
	byLocation := func(x, y model.Finding) int { return compareSource(x.Source, y.Source) }
	all := sorted(rp.Findings, byLocation)
	for _, g := range findingGroups {
		var rows [][]string
		for _, f := range all {
			switch {
			case f.Kind != g.kind:
			case f.Kind == "secret":
				rows = append(rows, []string{text(f.Severity), code(f.SecretType), secretLocation(f.Source)})
			default:
				rows = append(rows, []string{text(f.Severity), text(f.Text), source(f.Source)})
			}
		}
		label, header := g.label, []string{"Severity", "Finding", "Source"}
		switch g.kind {
		case "secret":
			header = []string{"Severity", "Type", "Location"}
		case "todo":
			label = fmt.Sprintf("%s (%d, at most %d are kept)", label, len(rows), maxTodo)
		}
		block(b, label, header, rows)
	}
}

func renderUI(b *bytes.Buffer, _ model.Report, rp model.Repo) {
	if len(rp.UI) == 0 {
		absent(b, rp, secUI, "user interface strings and screens", "")
		return
	}
	b.WriteString("Inventory only: strings and screens are listed where they are defined, never rendered or judged.\n\n")
	byKind := func(x, y model.UIItem) int {
		return cmp.Or(cmp.Compare(x.Kind, y.Kind), compareSource(x.Source, y.Source))
	}
	var rows [][]string
	for _, u := range sorted(rp.UI, byKind) {
		rows = append(rows, []string{text(u.Kind), text(u.Text), source(u.Source)})
	}
	table(b, []string{"Kind", "Text", "Source"}, rows)
}

// modelClause is the sentence a model-written section adds to its absence line when the scan used no model.
func modelClause(rp model.Repo, what string) string {
	if rp.Narrative.Model != "" {
		return ""
	}
	return what + " are written by a model, and " + modelOff
}

func renderOpenQuestions(b *bytes.Buffer, _ model.Report, rp model.Repo) {
	if len(rp.OpenQuestions) == 0 {
		absent(b, rp, secOpenQuestions, "questions the scan could not settle from the files", modelClause(rp, "Open questions"))
		return
	}
	var rows [][]string
	for _, q := range rp.OpenQuestions {
		options := make([]string, len(q.Options))
		for i, o := range q.Options {
			options[i] = fmt.Sprintf("(%d) %s", i+1, text(o))
		}
		rows = append(rows, []string{text(q.Topic), text(q.Question), source(q.Context), strings.Join(options, "; ")})
	}
	table(b, []string{"Topic", "Question", "Context", "Options"}, rows)
}

func renderRecommendations(b *bytes.Buffer, _ model.Report, rp model.Repo) {
	if len(rp.Recommendations) == 0 {
		absent(b, rp, secRecommendations, "facts this report can feed into the workspace's documents", modelClause(rp, "Recommendations"))
		return
	}
	var rows [][]string
	for _, r := range rp.Recommendations {
		rows = append(rows, []string{text(r.Text), sources(r.Citations)})
	}
	table(b, []string{"Recommendation", "Citations"}, rows)
}
