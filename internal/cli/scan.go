package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/saphan-ai/discover/internal/attest"
	"github.com/saphan-ai/discover/internal/manifest"
)

const scanUsage = "usage: discover scan [--out <dir>] [--record-root] [--git-timeout <duration>] <path>..."

// What the read-only proof covers, printed beside EQUAL so nobody reads it as wider than it is.
const (
	scopeGit   = "(covers tracked and untracked files, modes, HEAD; not ignored paths or .git internals)"
	scopePlain = "(covers files, symbolic links and the owner-execute bit; not empty directories)"
)

// Names of the files scan writes into --out.
const (
	manifestFile    = "READ_MANIFEST.json"
	attestationFile = "ATTESTATION.json"
	attestSchema    = "discover-attestation/v1"
)

// betweenPasses runs after the first digest and before the second. Tests use it to change the target in between.
var betweenPasses = func(root string) {}

type proof struct {
	Status  string   `json:"status"`
	Scope   string   `json:"scope"`
	Before  string   `json:"before"`
	After   string   `json:"after"`
	Changed []string `json:"changed,omitempty"`
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
	gitTimeout := fs.Duration("git-timeout", attest.DefaultGitTimeout, "deadline of each git call")
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
	if *gitTimeout <= 0 {
		fmt.Fprintf(stderr, "discover scan: --git-timeout must be positive, got %s\n", *gitTimeout)
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
		if inside(outAbs, t.real) {
			fmt.Fprintf(stderr, "discover scan: refused: --out %q lies inside the target %q; nothing was read or written. "+
				"Choose a directory outside every target.\n", *out, t.label)
			return ExitUsage
		}
	}

	// Every output directory is fixed, and checked against every target, before anything is read or written.
	used := map[string]bool{}
	dirs := make([]string, len(targets))
	for i, t := range targets {
		dirs[i] = outAbs
		if len(targets) > 1 {
			dirs[i] = filepath.Join(outAbs, freeName(used, t.label))
		}
	}
	for i, t := range targets {
		for _, o := range targets {
			if inside(dirs[i], o.real) {
				fmt.Fprintf(stderr, "discover scan: refused: the output directory of target %q lies inside the target %q; "+
					"nothing was read or written. Choose an --out directory whose per-target directories are outside every target.\n",
					t.label, o.label)
				return ExitUsage
			}
		}
	}

	// The scan never writes through a link: what already stands where it will write is a plain directory and plain
	// files, or the scan stops before it reads anything.
	for i, t := range targets {
		if msg := unplainOutput(dirs[i]); msg != "" {
			fmt.Fprintf(stderr, "discover scan: refused: the output %s of target %q; nothing was read or written. "+
				"Remove it, or choose another --out.\n", msg, t.label)
			return ExitUsage
		}
	}

	code := ExitOK
	for i, t := range targets {
		c, err := scanOne(t, targets, dirs[i], attest.Options{RecordRoot: *recordRoot, GitTimeout: *gitTimeout}, stdout)
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

func scanOne(t target, all []target, outDir string, opts attest.Options, stdout io.Writer) (int, error) {
	before, err := attest.Attest(t.real, opts)
	if err != nil {
		return ExitUsage, err
	}
	// The manifest lists what analysers read and cite; the attestation's own reads are covered by the digest.
	m := manifest.New(t.real)
	betweenPasses(t.real)
	after, err := attest.Attest(t.real, opts)
	if err != nil {
		return ExitUsage, err
	}
	diffs := attest.Compare(before, after)

	scope := scopePlain
	if before.Mode == attest.ModeGit {
		scope = scopeGit
	}
	doc := attestationDoc{Schema: attestSchema, Attestation: before,
		Proof: proof{Status: "EQUAL", Scope: scope, Before: before.Tree.Digest, After: after.Tree.Digest}}
	if len(diffs) > 0 {
		doc.Proof.Status = "NOT-EQUAL"
		for _, d := range diffs {
			doc.Proof.Changed = append(doc.Proof.Changed, d.Field)
		}
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return ExitUsage, err
	}
	// A directory or a link that appeared since the checks is caught here, on the directory as it now is.
	if o, bad := createdDirInsideTarget(outDir, all); bad {
		return ExitUsage, fmt.Errorf("refused: the output directory lies inside the target %q; nothing was written", o.label)
	}
	mj, err := m.JSON()
	if err != nil {
		return ExitUsage, err
	}
	aj, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return ExitUsage, err
	}
	if err := writeOutputs(outDir, map[string][]byte{manifestFile: mj, attestationFile: append(aj, '\n')}); err != nil {
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
		fmt.Fprintf(stdout, "read-only proof: EQUAL %s %s\n", before.Tree.Digest, scope)
		return ExitOK, nil
	}
	for _, d := range diffs {
		suffix := ""
		if d.Field != "tree" {
			suffix = " (" + d.Field + ")"
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

// inside reports whether p is the directory root or lies under it. It compares identities, not spellings: a path
// with another letter case on a case-insensitive file system, or through a hard-to-see alias, names the same
// directory. It walks up from p and asks each existing ancestor whether it is root.
func inside(p, root string) bool {
	rfi, err := os.Stat(root)
	if err != nil {
		return false
	}
	for cur := p; ; {
		if fi, err := os.Stat(cur); err == nil && os.SameFile(fi, rfi) {
			return true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return false
		}
		cur = parent
	}
}

// unplainOutput says what is wrong with an existing output path, or returns "" when nothing stands there or it is
// as it should be: a directory for dir, a regular file for each output file. A link is wrong wherever it points.
// It names labels and output file names only, never a path.
func unplainOutput(dir string) string {
	fi, err := os.Lstat(dir)
	if err != nil {
		return ""
	}
	if !fi.IsDir() {
		return "directory is a symbolic link or not a directory"
	}
	for _, name := range []string{manifestFile, attestationFile} {
		if fi, err := os.Lstat(filepath.Join(dir, name)); err == nil && !fi.Mode().IsRegular() {
			return "file " + name + " is a symbolic link or not a regular file"
		}
	}
	return ""
}

// createdDirInsideTarget reports the target that dir lies inside, judged on the real path of dir as it is now.
func createdDirInsideTarget(dir string, targets []target) (target, bool) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		real = dir
	}
	for _, t := range targets {
		if inside(real, t.real) {
			return t, true
		}
	}
	return target{}, false
}

// writeOutputs writes each file through a root opened on dir, so no write can leave dir, even when a link appears
// after the checks. Each file is created or truncated as a regular file.
func writeOutputs(dir string, files map[string][]byte) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, name := range []string{manifestFile, attestationFile} {
		f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		_, werr := f.Write(files[name])
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return werr
		}
	}
	return nil
}

// freeName returns label, or the first label-N (N from 2) that no earlier target took, and marks it taken.
func freeName(used map[string]bool, label string) string {
	if label == "/" || label == "." || label == string(filepath.Separator) {
		label = "root"
	}
	name := label
	for n := 2; used[name]; n++ {
		name = fmt.Sprintf("%s-%d", label, n)
	}
	used[name] = true
	return name
}
