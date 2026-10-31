package cli

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

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

// removeTemp removes a temporary output file that was not renamed into place. Tests replace it to make the cleanup fail.
var removeTemp = func(root *os.Root, name string) error { return root.Remove(name) }

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
				"Choose a directory outside every target.\n", filepath.Base(*out), t.label)
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

	// The scan never writes into an existing file or through a link: what already stands where it will write is a
	// plain directory and plain files with one name each, or the scan stops before it reads anything.
	for i, t := range targets {
		if msg := unplainOutput(dirs[i]); msg != "" {
			fmt.Fprintf(stderr, "discover scan: refused: the output %s of target %q; nothing was read or written. "+
				"Remove it, or choose another --out.\n", msg, t.label)
			return ExitUsage
		}
	}

	// The output directories are made, and held open, before any target is read. Nothing below resolves them by path
	// again to create or write: a link that appears later cannot move the scan's writes into a target.
	outs := make([]outputDir, len(targets))
	defer func() {
		for _, o := range outs {
			if o.root != nil {
				o.root.Close()
			}
		}
	}()
	var created []string
	for i, t := range targets {
		o, made, err := openOutput(dirs[i], outAbs, targets)
		created = append(created, made...)
		if err != nil {
			where := fmt.Sprintf("--out %q", filepath.Base(*out))
			if dirs[i] != outAbs {
				rel, _ := filepath.Rel(outAbs, dirs[i])
				where = fmt.Sprintf("%q under --out %q", rel, filepath.Base(*out))
			}
			fmt.Fprintf(stderr, "discover scan: refused: the output directory of target %q (%s) %s; nothing was read or written; %s. "+
				"Choose another --out.\n", t.label, where, err, createdSentence(outAbs, created))
			return ExitUsage
		}
		o.created, o.base = made, outAbs
		outs[i] = o
	}

	code := ExitOK
	for i, t := range targets {
		c, err := scanOne(t, outs[i], attest.Options{RecordRoot: *recordRoot, GitTimeout: *gitTimeout}, stdout)
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

// outputDir is an output directory held open: root is the handle every write goes through, path is the name it was
// opened by (used only to see whether that name still leads to it), created lists what the scan had to make.
type outputDir struct {
	root    *os.Root
	path    string
	base    string
	created []string
}

func scanOne(t target, out outputDir, opts attest.Options, stdout io.Writer) (int, error) {
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
	// The directory was opened and judged before the target was read. A path that no longer names it is a refusal:
	// the write would land where the owner does not expect it.
	root := out.root
	if !stillNames(root, out.path) {
		return ExitUsage, fmt.Errorf("refused: the output directory was replaced while the target was read; no output file was written; %s. "+
			"Run the scan again", createdSentence(out.base, out.created))
	}
	mj, err := m.JSON()
	if err != nil {
		return ExitUsage, err
	}
	aj, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return ExitUsage, err
	}
	if err := writeOutputs(root, map[string][]byte{manifestFile: mj, attestationFile: append(aj, '\n')}); err != nil {
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
// as it should be: a directory for dir, a regular file with one name for each output file. A link is wrong wherever
// it points, and so is a second name of a file, because a write into it would change the file it shares.
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
		fi, err := os.Lstat(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if !fi.Mode().IsRegular() {
			return "file " + name + " is a symbolic link or not a regular file"
		}
		if n, ok := linkCount(fi); ok && n > 1 {
			return "file " + name + " is a hard link: it has more than one name"
		}
	}
	return ""
}

// openedDirInsideTarget reports the target that the opened output directory lies inside. The judgement is made on
// the identity of the handle: root's own directory, then the directories above the path that still names it.
func openedDirInsideTarget(root *os.Root, dir string, targets []target) (target, bool, error) {
	rs, err := root.Stat(".")
	if err != nil {
		return target{}, false, err
	}
	if !stillNames(root, dir) {
		return target{}, false, errors.New("was replaced while it was opened")
	}
	return dirInsideTarget(rs, dir, targets)
}

// stillNames reports whether the path dir leads to the directory root holds. It only looks; it creates nothing.
func stillNames(root *os.Root, dir string) bool {
	rs, err := root.Stat(".")
	if err != nil {
		return false
	}
	ps, err := os.Stat(dir)
	return err == nil && os.SameFile(rs, ps)
}

// openOutput makes the output directory dir and returns it held open. It opens the deepest directory that already
// exists, judges that opened handle against every target, makes the missing directories one at a time through that
// handle (a link inside it that points out is refused), opens the output directory through it and judges that too.
// It returns every directory it made, also when it refuses. Its errors name labels and reasons, never a path.
func openOutput(dir, outAbs string, targets []target) (outputDir, []string, error) {
	anc, missing := dir, []string(nil)
	for {
		fi, err := os.Stat(anc)
		if err == nil {
			if !fi.IsDir() {
				return outputDir{}, nil, fmt.Errorf("cannot be made: %q is not a directory", filepath.Base(anc))
			}
			break
		}
		parent := filepath.Dir(anc)
		if parent == anc {
			return outputDir{}, nil, fmt.Errorf("cannot be made (%s)", reasonOf(err))
		}
		missing = append([]string{filepath.Base(anc)}, missing...)
		anc = parent
	}
	parent, err := os.OpenRoot(anc)
	if err != nil {
		return outputDir{}, nil, fmt.Errorf("cannot be opened (%s)", reasonOf(err))
	}
	defer parent.Close()
	if o, bad, err := openedDirInsideTarget(parent, anc, targets); err != nil {
		return outputDir{}, nil, fmt.Errorf("cannot be made: its existing parent %w", err)
	} else if bad {
		return outputDir{}, nil, fmt.Errorf("lies inside the target %q", o.label)
	}
	var created []string
	rel := "."
	for _, name := range missing {
		next := filepath.Join(rel, name)
		if err := parent.Mkdir(next, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return outputDir{}, created, fmt.Errorf("cannot be made (%s)", reasonOf(err))
		} else if err == nil {
			created = append(created, filepath.Join(anc, next))
		}
		rel = next
	}
	sub, err := parent.OpenRoot(rel)
	if err != nil {
		return outputDir{}, created, fmt.Errorf("cannot be opened (%s)", reasonOf(err))
	}
	if o, bad, err := openedDirInsideTarget(sub, dir, targets); err != nil || bad {
		sub.Close()
		if err != nil {
			return outputDir{}, created, fmt.Errorf("%w", err)
		}
		return outputDir{}, created, fmt.Errorf("lies inside the target %q", o.label)
	}
	return outputDir{root: sub, path: dir}, created, nil
}

// reasonOf is the operating system's reason for err without the path the error carries.
func reasonOf(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	var en syscall.Errno
	if errors.As(err, &en) {
		return en.Error()
	}
	return "unexpected error"
}

// createdSentence says what the scan made and left in place, by name: relative to --out where the directory lies
// inside it, else by its own name.
func createdSentence(outAbs string, created []string) string {
	if len(created) == 0 {
		return "no directory was created"
	}
	names := make([]string, len(created))
	for i, p := range created {
		name := filepath.Base(p)
		if rel, err := filepath.Rel(outAbs, p); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			name = rel
		}
		names[i] = fmt.Sprintf("%q", name)
	}
	return "directories created and left in place: " + strings.Join(names, ", ")
}

// dirInsideTarget judges a directory whose identity is rs and whose path is dir.
func dirInsideTarget(rs fs.FileInfo, dir string, targets []target) (target, bool, error) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		real = dir
	}
	for _, t := range targets {
		if tfi, err := os.Stat(t.real); err == nil && os.SameFile(rs, tfi) {
			return t, true, nil
		}
		if inside(filepath.Dir(real), t.real) {
			return t, true, nil
		}
	}
	return target{}, false, nil
}

// writeOutputs writes each file under a fresh name in root and renames it into place. It never opens an existing
// output name for writing, so a hard link, a link or a FIFO standing at that name is replaced, never written into.
func writeOutputs(root *os.Root, files map[string][]byte) error {
	for _, name := range []string{manifestFile, attestationFile} {
		if err := writeFresh(root, name, files[name]); err != nil {
			return outputError(name, err)
		}
	}
	return nil
}

func writeFresh(root *os.Root, name string, data []byte) error {
	tmp, f, err := createFresh(root)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = root.Rename(tmp, name)
	}
	if err != nil {
		if rerr := removeTemp(root, tmp); rerr != nil {
			return &leftoverError{cause: err, tmp: tmp, reason: reasonOf(rerr)}
		}
	}
	return err
}

// leftoverError is a failed write whose temporary file could not be removed either.
type leftoverError struct {
	cause  error
	tmp    string
	reason string
}

func (e *leftoverError) Error() string { return e.cause.Error() }
func (e *leftoverError) Unwrap() error { return e.cause }

// createFresh makes a file that did not exist: O_EXCL fails on any existing name, a link included.
func createFresh(root *os.Root) (string, *os.File, error) {
	var last error
	for i := 0; i < 8; i++ {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", nil, err
		}
		tmp := ".discover-" + hex.EncodeToString(b[:]) + ".tmp"
		f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			return tmp, f, nil
		}
		last = err
	}
	return "", nil, last
}

// outputError says, in a sentence, which output file could not be put in place and what to do. It carries the
// operating system's reason and no path.
func outputError(name string, err error) error {
	reason := "the write failed"
	var en syscall.Errno
	if errors.As(err, &en) {
		reason = en.Error()
	}
	left := ""
	var lo *leftoverError
	if errors.As(err, &lo) {
		left = fmt.Sprintf(" The temporary file %s, in the output directory, could not be removed (%s); remove it.", lo.tmp, lo.reason)
	}
	return fmt.Errorf("refused: the output file %s could not be put in place (%s); the target was not changed. "+
		"Remove what stands at that name, or choose another --out.%s", name, reason, left)
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
