// Package attest states what a target was when discover began to read it, and recomputes it at the end.
//
// A target is a git repository (branch, HEAD, clean or dirty) or a plain directory. For both, the attestation holds
// the file count, the total bytes and the tree digest specified in docs/DIGEST.md. Nothing here writes to the
// target: files are opened read-only through a confined root, no mode or time is changed, and git runs with optional
// locks off. Nothing here asks git to compare the work tree, because that is what makes git run the programs the
// repository's own configuration names (clean filters, in the repository and in its submodules): the dirty state is
// derived from the index and from discover's own hashing instead.
package attest

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // git's own object id for a blob, never a security digest
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/saphan-ai/discover/internal/safepath"
)

// Mode is how a target was read.
type Mode string

const (
	// ModeGit is a directory with a .git entry at its root.
	ModeGit Mode = "git"
	// ModeDir is any other directory: a legacy snapshot.
	ModeDir Mode = "dir"
)

// DefaultGitTimeout bounds every git call unless Options.GitTimeout says otherwise.
const DefaultGitTimeout = 60 * time.Second

// Options tune one attestation.
type Options struct {
	// RecordRoot records the absolute root. By default only its base name is recorded.
	RecordRoot bool
	// GitBin is the git executable; empty means "git".
	GitBin string
	// GitTimeout is the deadline of each git call; zero means DefaultGitTimeout.
	GitTimeout time.Duration
}

// Tree is the digest of one set of files.
type Tree struct {
	Files      int    `json:"files"`
	Bytes      int64  `json:"bytes"`
	Links      int    `json:"links"`
	Submodules int    `json:"submodules"`
	Unreadable int    `json:"unreadable"`
	Digest     string `json:"digest"`
}

// GitState is what git says about the target. It never carries the content of a dirty path.
type GitState struct {
	Branch     string `json:"branch"`
	Detached   bool   `json:"detached"`
	Head       string `json:"head"`
	Clean      bool   `json:"clean"`
	DirtyPaths int    `json:"dirty_paths"`
}

// Attestation is the state of one target at one moment.
type Attestation struct {
	Root      string    `json:"root"`
	Mode      Mode      `json:"mode"`
	Git       *GitState `json:"git,omitempty"`
	Tree      Tree      `json:"tree"`
	Untracked *Tree     `json:"untracked,omitempty"`
}

// scrubbed is an error whose text no longer names the absolute root.
type scrubbed struct {
	msg string
	err error
}

func (e *scrubbed) Error() string { return e.msg }
func (e *scrubbed) Unwrap() error { return e.err }

// Attest reads root and returns its attestation. root must be an existing directory. An error names the target by
// its base name, never by its absolute path.
func Attest(root string, opts Options) (Attestation, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return Attestation{}, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return Attestation{}, scrub(err, filepath.Base(abs), abs)
	}
	a, err := attest(abs, real, opts)
	if err != nil {
		err = scrub(err, filepath.Base(abs), real, abs)
	}
	return a, err
}

// scrub replaces every spelling of the root in the error text by the label.
func scrub(err error, label string, roots ...string) error {
	msg := err.Error()
	for _, r := range roots {
		if r != "" {
			msg = strings.ReplaceAll(msg, r, label)
		}
	}
	if msg == err.Error() {
		return err
	}
	return &scrubbed{msg: msg, err: err}
}

func attest(abs, real string, opts Options) (Attestation, error) {
	fi, err := os.Stat(real)
	if err != nil {
		return Attestation{}, err
	}
	if !fi.IsDir() {
		return Attestation{}, fmt.Errorf("%s: not a directory", filepath.Base(abs))
	}
	a := Attestation{Root: filepath.Base(abs)}
	if opts.RecordRoot {
		a.Root = abs
	}
	root, err := safepath.OpenRoot(real)
	if err != nil {
		return a, err
	}
	defer root.Close()
	if _, err := root.Lstat(".git"); err == nil {
		a.Mode = ModeGit
		return a, attestGit(real, root, opts, &a)
	}
	a.Mode = ModeDir
	a.Tree, err = scanDir(root)
	return a, err
}

// Diff is one thing that differs between two attestations.
type Diff struct {
	// Field names what differs: tree, untracked, mode, head, branch, detached or dirty-paths.
	Field string
	// Untracked is true for the untracked digest.
	Untracked     bool
	Before, After string
}

// Compare lists what differs. An empty result means the target is unchanged as far as the proof reaches.
func Compare(before, after Attestation) []Diff {
	var d []Diff
	add := func(field, b, a string) {
		d = append(d, Diff{Field: field, Untracked: field == "untracked", Before: b, After: a})
	}
	if before.Mode != after.Mode {
		add("mode", string(before.Mode), string(after.Mode))
	}
	if before.Tree.Digest != after.Tree.Digest {
		add("tree", before.Tree.Digest, after.Tree.Digest)
	}
	switch bu, au := before.Untracked, after.Untracked; {
	case bu == nil && au == nil:
	case bu == nil || au == nil:
		add("untracked", digestOrAbsent(bu), digestOrAbsent(au))
	case bu.Digest != au.Digest:
		add("untracked", bu.Digest, au.Digest)
	}
	if bg, ag := before.Git, after.Git; bg != nil && ag != nil {
		if bg.Head != ag.Head {
			add("head", bg.Head, ag.Head)
		}
		if bg.Branch != ag.Branch {
			add("branch", bg.Branch, ag.Branch)
		}
		if bg.Detached != ag.Detached {
			add("detached", fmt.Sprint(bg.Detached), fmt.Sprint(ag.Detached))
		}
		if bg.DirtyPaths != ag.DirtyPaths {
			add("dirty-paths", fmt.Sprint(bg.DirtyPaths), fmt.Sprint(ag.DirtyPaths))
		}
	}
	return d
}

func digestOrAbsent(t *Tree) string {
	if t == nil {
		return "absent"
	}
	return t.Digest
}

// ---- digest ----

// esc makes a path or a link target safe to put in a line: a backslash, a newline and a `>` are escaped, so no name
// can end a line early or forge the ` -> ` that separates a link from its target. The encoding is injective.
var esc = strings.NewReplacer(`\`, `\\`, "\n", `\n`, ">", `\>`).Replace

// observed is what a tracked path was in the work tree, in git's vocabulary.
type observed struct {
	dir  bool   // a directory (a submodule's work tree)
	mode string // 100644, 100755 or 120000
	oid  string // git's object id of the content, no filters applied
}

type builder struct {
	root   *safepath.Root
	lines  []string
	t      Tree
	oidLen int // 40 or 64 when git object ids are computed as well; 0 otherwise
	obs    map[string]observed
}

func (b *builder) finish() Tree {
	sort.Strings(b.lines) // bytewise, the same order as LC_ALL=C sort
	h := sha256.New()
	for _, l := range b.lines {
		io.WriteString(h, l)
		io.WriteString(h, "\n")
	}
	b.t.Digest = hex.EncodeToString(h.Sum(nil))
	return b.t
}

func (b *builder) unreadable(rel string) {
	b.t.Unreadable++
	b.lines = append(b.lines, "unreadable  "+esc(rel))
}

func (b *builder) gitHash(size int) hash.Hash {
	var h hash.Hash
	switch b.oidLen {
	case 40:
		h = sha1.New() //nolint:gosec // see the import
	case 64:
		h = sha256.New()
	default:
		return nil
	}
	fmt.Fprintf(h, "blob %d\x00", size)
	return h
}

func (b *builder) link(rel string) {
	target, err := b.root.Readlink(rel)
	if err != nil {
		b.unreadable(rel)
		return
	}
	b.t.Links++
	b.lines = append(b.lines, "link  "+esc(rel)+" -> "+esc(target))
	if gh := b.gitHash(len(target)); gh != nil {
		io.WriteString(gh, target)
		b.obs[rel] = observed{mode: "120000", oid: hex.EncodeToString(gh.Sum(nil))}
	}
}

func (b *builder) file(rel string) {
	f, fi, err := b.root.OpenRegular(rel)
	if err != nil {
		b.unreadable(rel)
		return
	}
	defer f.Close()
	h := sha256.New()
	var w io.Writer = h
	gh := b.gitHash(int(fi.Size()))
	if gh != nil {
		w = io.MultiWriter(h, gh)
	}
	n, err := io.Copy(w, f)
	if err != nil || (gh != nil && n != fi.Size()) { // a file that changes under the read is not a reading
		b.unreadable(rel)
		return
	}
	mode, gitMode := "-", "100644"
	if fi.Mode()&0o100 != 0 {
		mode, gitMode = "x", "100755"
	}
	b.t.Files++
	b.t.Bytes += n
	b.lines = append(b.lines, hex.EncodeToString(h.Sum(nil))+"  "+mode+"  "+esc(rel))
	if gh != nil {
		b.obs[rel] = observed{mode: gitMode, oid: hex.EncodeToString(gh.Sum(nil))}
	}
}

// scanDir digests every regular file under root. A symlink is never followed.
func scanDir(root *safepath.Root) (Tree, error) {
	b := builder{root: root}
	err := fs.WalkDir(root.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
		if rel == "." {
			return err
		}
		if err != nil {
			b.unreadable(rel)
			return nil
		}
		switch t := d.Type(); {
		case t&fs.ModeSymlink != 0:
			b.link(rel)
		case t.IsRegular():
			b.file(rel)
		}
		return nil
	})
	if err != nil {
		return Tree{}, err
	}
	return b.finish(), nil
}

// digestList digests the named paths of a git target. With oidLen set it also returns what each path was.
func digestList(root *safepath.Root, rels []string, oidLen int) (Tree, map[string]observed) {
	b := builder{root: root, oidLen: oidLen, obs: map[string]observed{}}
	seen := map[string]bool{}
	for _, rel := range rels {
		rel = strings.TrimSuffix(rel, "/")
		if rel == "" || seen[rel] {
			continue
		}
		seen[rel] = true
		if err := root.Check(rel); err != nil {
			b.unreadable(rel)
			continue
		}
		fi, err := root.Lstat(rel)
		switch {
		case err != nil:
			b.unreadable(rel)
		case fi.Mode()&os.ModeSymlink != 0:
			b.link(rel)
		case fi.IsDir():
			b.t.Submodules++
			b.lines = append(b.lines, "submodule  "+esc(rel))
			b.obs[rel] = observed{dir: true}
		case fi.Mode().IsRegular():
			b.file(rel)
		default:
			b.unreadable(rel)
		}
	}
	return b.finish(), b.obs
}

// ---- git ----

// gitEnv is the environment of every git call: the caller's, with every GIT_* variable removed, because a caller with
// GIT_DIR set (a git hook, many CI steps) would otherwise make git read another repository, and GIT_CONFIG_* would
// make it obey another configuration. The scan's own settings follow: no optional lock, no prompt, no lazy fetch from
// a promisor remote (which would run what the target's configuration names), and a ceiling at the parent of root so
// git never discovers a repository above it.
func gitEnv(caller []string, root string) []string {
	env := make([]string, 0, len(caller)+4)
	for _, kv := range caller {
		if !strings.HasPrefix(strings.ToUpper(kv), "GIT_") {
			env = append(env, kv)
		}
	}
	return append(env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1",
		"GIT_CEILING_DIRECTORIES="+filepath.Dir(root))
}

// minGitMajor and minGitMinor are the first git that honours GIT_NO_LAZY_FETCH.
const (
	minGitMajor = 2
	minGitMinor = 45
)

// parseGitVersion reads the output of `git version`: "git version 2.50.1 (Apple Git-155)" gives "2.50.1", 2, 50.
func parseGitVersion(out string) (ver string, major, minor int, ok bool) {
	f := strings.Fields(out)
	if len(f) < 3 || f[0] != "git" || f[1] != "version" {
		return "", 0, 0, false
	}
	ver = f[2]
	nums := strings.SplitN(ver, ".", 3)
	if len(nums) < 2 {
		return "", 0, 0, false
	}
	var err error
	if major, err = strconv.Atoi(leadingDigits(nums[0])); err != nil {
		return "", 0, 0, false
	}
	if minor, err = strconv.Atoi(leadingDigits(nums[1])); err != nil {
		return "", 0, 0, false
	}
	return ver, major, minor, true
}

func leadingDigits(s string) string {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return s[:n]
}

// checkGit refuses a git that would fetch, and a root git does not accept as the top of a repository.
func checkGit(root string, opts Options) error {
	out, err := runGit(root, opts, "version")
	if err != nil {
		return err
	}
	ver, major, minor, ok := parseGitVersion(string(out))
	if !ok {
		return fmt.Errorf("cannot read the git version from %q", strings.TrimSpace(string(out)))
	}
	if major < minGitMajor || (major == minGitMajor && minor < minGitMinor) {
		return fmt.Errorf("git %d.%d or newer is required to read a repository without letting it fetch; found %s",
			minGitMajor, minGitMinor, ver)
	}
	top, err := runGit(root, opts, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("%s: a .git entry is present but git does not accept it as a repository; git mode refused",
			filepath.Base(root))
	}
	tfi, terr := os.Stat(strings.TrimSpace(string(top)))
	rfi, rerr := os.Stat(root)
	if terr != nil || rerr != nil || !os.SameFile(tfi, rfi) {
		return fmt.Errorf("%s: git resolves this directory to another repository; git mode refused", filepath.Base(root))
	}
	return nil
}

// runGit runs one git call under a deadline. A call that overruns it is killed and the error names the call.
func runGit(root string, opts Options, args ...string) ([]byte, error) {
	bin := opts.GitBin
	if bin == "" {
		bin = "git"
	}
	timeout := opts.GitTimeout
	if timeout <= 0 {
		timeout = DefaultGitTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	full := append([]string{"-C", root, "-c", "core.fsmonitor=false", "--no-optional-locks"}, args...)
	cmd := exec.CommandContext(ctx, bin, full...)
	cmd.WaitDelay = time.Second
	cmd.Env = gitEnv(os.Environ(), root)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("git %s timed out after %s (raise the deadline with --git-timeout)", strings.Join(args, " "), timeout)
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return out.Bytes(), fmt.Errorf("git %s: %w", args[0], err)
		}
		return nil, err
	}
	return out.Bytes(), nil
}

func nulList(b []byte) []string {
	var out []string
	for _, s := range strings.Split(string(b), "\x00") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// gitEntry is one record of `git ls-files -s -z` or `git ls-tree -r -z`.
type gitEntry struct {
	mode, oid string
	stage     string
}

// parseEntries reads records of the form `<mode> <a> <b>\t<path>`: for ls-files -s, a is the object id and b the
// stage; for ls-tree, a is the type and b the object id.
func parseEntries(b []byte, tree bool) (map[string]gitEntry, []string, error) {
	m := map[string]gitEntry{}
	var order []string
	for _, rec := range nulList(b) {
		meta, path, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 {
			return nil, nil, fmt.Errorf("unexpected git output %q", meta)
		}
		e := gitEntry{mode: f[0], oid: f[1], stage: f[2]}
		if tree {
			e = gitEntry{mode: f[0], oid: f[2], stage: "0"}
		}
		if _, dup := m[path]; !dup {
			order = append(order, path)
		}
		if old, dup := m[path]; !dup || e.stage < old.stage {
			m[path] = e // an unmerged path keeps its lowest stage, which is never "0"
		}
	}
	return m, order, nil
}

func attestGit(root string, r *safepath.Root, opts Options, a *Attestation) error {
	if err := checkGit(root, opts); err != nil {
		return err
	}
	g := &GitState{}
	if out, err := runGit(root, opts, "rev-parse", "--verify", "-q", "HEAD"); err == nil {
		g.Head = strings.TrimSpace(string(out))
	}
	if out, err := runGit(root, opts, "symbolic-ref", "-q", "--short", "HEAD"); err == nil {
		g.Branch = strings.TrimSpace(string(out))
	} else {
		g.Detached = true
	}

	idxOut, err := runGit(root, opts, "ls-files", "-s", "-z")
	if err != nil {
		return err
	}
	index, tracked, err := parseEntries(idxOut, false)
	if err != nil {
		return err
	}
	headTree := map[string]gitEntry{}
	if g.Head != "" {
		out, err := runGit(root, opts, "ls-tree", "-r", "-z", g.Head)
		if err != nil {
			return err
		}
		if headTree, _, err = parseEntries(out, true); err != nil {
			return err
		}
	}
	oidLen := 0
	for _, e := range index {
		oidLen = len(e.oid)
		break
	}

	var obs map[string]observed
	a.Tree, obs = digestList(r, tracked, oidLen)

	dirty := map[string]bool{}
	for path, e := range index { // the index against HEAD, and an unmerged path
		if h, ok := headTree[path]; e.stage != "0" || !ok || h.mode != e.mode || h.oid != e.oid {
			dirty[path] = true
		}
	}
	for path := range headTree {
		if _, ok := index[path]; !ok {
			dirty[path] = true
		}
	}
	for path, e := range index { // the work tree against the index, on the raw bytes: no filter is applied
		o, ok := obs[path]
		switch {
		case !ok:
			dirty[path] = true
		case e.mode == "160000":
			dirty[path] = dirty[path] || !o.dir
		case o.dir || o.mode != e.mode || o.oid != e.oid:
			dirty[path] = true
		}
	}

	collapsed, err := runGit(root, opts, "ls-files", "-z", "--others", "--exclude-standard", "--directory", "--no-empty-directory")
	if err != nil {
		return err
	}
	for _, p := range nulList(collapsed) {
		dirty[p] = true
	}
	g.DirtyPaths = len(dirty)
	g.Clean = g.DirtyPaths == 0
	a.Git = g

	others, err := runGit(root, opts, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return err
	}
	u, _ := digestList(r, nulList(others), 0)
	a.Untracked = &u
	return nil
}
