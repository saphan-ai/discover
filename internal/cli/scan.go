package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/saphan-ai/discover/internal/attest"
	"github.com/saphan-ai/discover/internal/manifest"
)

const scanUsage = "usage: discover scan [--out <dir>] [--record-root] <path>..."

// Names of the files scan writes into --out.
const (
	manifestFile    = "READ_MANIFEST.json"
	attestationFile = "ATTESTATION.json"
	attestSchema    = "discover-attestation/v1"
)

// betweenPasses runs after the first digest and before the second. Tests use it to change the target in between.
var betweenPasses = func(root string) {}

type proof struct {
	Status string `json:"status"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type attestationDoc struct {
	Schema      string             `json:"schema"`
	Attestation attest.Attestation `json:"attestation"`
	Proof       proof              `json:"read_only_proof"`
}

type target struct {
	given string
	real  string
	label string
}

func scan(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "./discover-out", "directory for READ_MANIFEST.json and ATTESTATION.json")
	recordRoot := fs.Bool("record-root", false, "record the absolute root path instead of its base name")
	var given []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return ExitOK
			}
			return ExitUsage
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		given = append(given, rest[0])
		rest = rest[1:]
	}
	if len(given) == 0 {
		fmt.Fprintln(stderr, "discover scan: no target given")
		fmt.Fprintln(stderr, scanUsage)
		return ExitUsage
	}

	var targets []target
	for _, g := range given {
		abs, err := filepath.Abs(g)
		if err != nil {
			fmt.Fprintf(stderr, "discover scan: %v\n", err)
			return ExitUsage
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			fmt.Fprintf(stderr, "discover scan: cannot read target %q\n", filepath.Base(abs))
			return ExitUsage
		}
		if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
			fmt.Fprintf(stderr, "discover scan: target %q is not a directory\n", filepath.Base(abs))
			return ExitUsage
		}
		targets = append(targets, target{given: g, real: real, label: filepath.Base(abs)})
	}

	outAbs, err := resolveLoose(*out)
	if err != nil {
		fmt.Fprintf(stderr, "discover scan: %v\n", err)
		return ExitUsage
	}
	for _, t := range targets {
		if within(outAbs, t.real) {
			fmt.Fprintf(stderr, "discover scan: refused: --out %q lies inside the target %q; nothing was read or written. "+
				"Choose a directory outside every target.\n", *out, t.label)
			return ExitUsage
		}
	}

	used := map[string]int{}
	code := ExitOK
	for _, t := range targets {
		dir := outAbs
		if len(targets) > 1 {
			used[t.label]++
			name := t.label
			if n := used[t.label]; n > 1 {
				name = fmt.Sprintf("%s-%d", t.label, n)
			}
			dir = filepath.Join(outAbs, name)
		}
		c, err := scanOne(t, dir, attest.Options{RecordRoot: *recordRoot}, stdout)
		if err != nil {
			fmt.Fprintf(stderr, "discover scan: %q: %v\n", t.label, err)
			return ExitUsage
		}
		if c > code {
			code = c
		}
	}
	return code
}

func scanOne(t target, outDir string, opts attest.Options, stdout io.Writer) (int, error) {
	before, err := attest.Attest(t.real, opts)
	if err != nil {
		return ExitUsage, err
	}
	m := manifest.New(t.real)
	betweenPasses(t.real)
	after, err := attest.Attest(t.real, opts)
	if err != nil {
		return ExitUsage, err
	}
	diffs := attest.Compare(before, after)

	doc := attestationDoc{Schema: attestSchema, Attestation: before,
		Proof: proof{Status: "EQUAL", Before: before.Tree.Digest, After: after.Tree.Digest}}
	if len(diffs) > 0 {
		doc.Proof.Status = "NOT-EQUAL"
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return ExitUsage, err
	}
	mj, err := m.JSON()
	if err != nil {
		return ExitUsage, err
	}
	aj, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return ExitUsage, err
	}
	if err := os.WriteFile(filepath.Join(outDir, manifestFile), mj, 0o644); err != nil {
		return ExitUsage, err
	}
	if err := os.WriteFile(filepath.Join(outDir, attestationFile), append(aj, '\n'), 0o644); err != nil {
		return ExitUsage, err
	}

	fmt.Fprintf(stdout, "target: %s (%s)\n", before.Root, before.Mode)
	if g := before.Git; g != nil {
		branch := g.Branch
		if g.Detached {
			branch = "(detached)"
		}
		fmt.Fprintf(stdout, "branch: %s  head: %s\n", branch, g.Head)
		if g.Clean {
			fmt.Fprintln(stdout, "clean")
		} else {
			fmt.Fprintf(stdout, "dirty: %d paths\n", g.DirtyPaths)
		}
	}
	fmt.Fprintf(stdout, "files: %d  bytes: %d  digest: %s\n", before.Tree.Files, before.Tree.Bytes, before.Tree.Digest)
	if u := before.Untracked; u != nil && u.Files+u.Links+u.Submodules+u.Unreadable > 0 {
		fmt.Fprintf(stdout, "untracked: %d files  digest: %s\n", u.Files, u.Digest)
	}
	if len(diffs) == 0 {
		fmt.Fprintf(stdout, "read-only proof: EQUAL %s\n", before.Tree.Digest)
		return ExitOK, nil
	}
	for _, d := range diffs {
		suffix := ""
		if d.Untracked {
			suffix = " (untracked)"
		}
		fmt.Fprintf(stdout, "read-only proof: NOT-EQUAL %s %s%s\n", d.Before, d.After, suffix)
	}
	return ExitNotEqual, nil
}

// resolveLoose makes p absolute and resolves symlinks in the part of it that exists.
func resolveLoose(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	cur, tail := abs, ""
	for {
		real, err := filepath.EvalSymlinks(cur)
		if err == nil {
			return filepath.Join(real, tail), nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs, nil
		}
		tail = filepath.Join(filepath.Base(cur), tail)
		cur = parent
	}
}

// within reports whether p is root or lies under it.
func within(p, root string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
