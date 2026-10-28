// Package attest states what a target was when discover began to read it, and recomputes it at the end.
//
// A target is a git repository (branch, HEAD, clean or dirty) or a plain directory. For both, the attestation holds
// the file count, the total bytes and the tree digest specified in docs/DIGEST.md. Nothing here writes to the
// target: files are opened read-only, no mode or time is changed, and git runs with optional locks off.
package attest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

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

// Options tune one attestation.
type Options struct {
	// RecordRoot records the absolute root. By default only its base name is recorded.
	RecordRoot bool
	// GitBin is the git executable; empty means "git".
	GitBin string
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

// Attest reads root and returns its attestation. root must be an existing directory.
func Attest(root string, opts Options) (Attestation, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return Attestation{}, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return Attestation{}, err
	}
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
	if _, err := os.Lstat(filepath.Join(real, ".git")); err == nil {
		a.Mode = ModeGit
		return a, attestGit(real, opts, &a)
	}
	a.Mode = ModeDir
	a.Tree, err = scanDir(real)
	return a, err
}

// Diff is one digest that differs between two attestations.
type Diff struct {
	Untracked     bool
	Before, After string
}

// Compare lists the digests that differ. An empty result means the tree is unchanged.
func Compare(before, after Attestation) []Diff {
	var d []Diff
	if before.Tree.Digest != after.Tree.Digest {
		d = append(d, Diff{false, before.Tree.Digest, after.Tree.Digest})
	}
	if before.Untracked != nil && after.Untracked != nil && before.Untracked.Digest != after.Untracked.Digest {
		d = append(d, Diff{true, before.Untracked.Digest, after.Untracked.Digest})
	}
	return d
}

// ---- digest ----

type builder struct {
	lines []string
	t     Tree
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
	b.lines = append(b.lines, "unreadable  "+rel)
}

func (b *builder) link(abs, rel string) {
	target, err := os.Readlink(abs)
	if err != nil {
		b.unreadable(rel)
		return
	}
	b.t.Links++
	b.lines = append(b.lines, "link  "+rel+" -> "+target)
}

func (b *builder) file(abs, rel string) {
	f, _, err := safepath.OpenRegular(abs)
	if err != nil {
		b.unreadable(rel)
		return
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		b.unreadable(rel)
		return
	}
	b.t.Files++
	b.t.Bytes += n
	b.lines = append(b.lines, hex.EncodeToString(h.Sum(nil))+"  "+rel)
}

// scanDir digests every regular file under root. A symlink is never followed.
func scanDir(root string) (Tree, error) {
	var b builder
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return err
		}
		rel = filepath.ToSlash(rel)
		if err != nil {
			b.unreadable(rel)
			return nil
		}
		switch t := d.Type(); {
		case t&fs.ModeSymlink != 0:
			b.link(p, rel)
		case t.IsRegular():
			b.file(p, rel)
		}
		return nil
	})
	if err != nil {
		return Tree{}, err
	}
	return b.finish(), nil
}

// digestList digests the named paths of a git target.
func digestList(root string, rels []string) Tree {
	var b builder
	seen := map[string]bool{}
	for _, rel := range rels {
		rel = strings.TrimSuffix(rel, "/")
		if rel == "" || seen[rel] {
			continue
		}
		seen[rel] = true
		abs, err := safepath.Resolve(root, rel)
		if err != nil {
			b.unreadable(rel)
			continue
		}
		fi, err := os.Lstat(abs)
		switch {
		case err != nil:
			b.unreadable(rel)
		case fi.Mode()&os.ModeSymlink != 0:
			b.link(abs, rel)
		case fi.IsDir():
			b.t.Submodules++
			b.lines = append(b.lines, "submodule  "+rel)
		case fi.Mode().IsRegular():
			b.file(abs, rel)
		default:
			b.unreadable(rel)
		}
	}
	return b.finish()
}

// ---- git ----

func runGit(root string, opts Options, args ...string) ([]byte, error) {
	bin := opts.GitBin
	if bin == "" {
		bin = "git"
	}
	full := append([]string{"-C", root, "-c", "core.fsmonitor=false", "--no-optional-locks"}, args...)
	cmd := exec.Command(bin, full...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
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

func attestGit(root string, opts Options, a *Attestation) error {
	g := &GitState{}
	if out, err := runGit(root, opts, "rev-parse", "--verify", "-q", "HEAD"); err == nil {
		g.Head = strings.TrimSpace(string(out))
	}
	if out, err := runGit(root, opts, "symbolic-ref", "-q", "--short", "HEAD"); err == nil {
		g.Branch = strings.TrimSpace(string(out))
	} else {
		g.Detached = true
	}
	st, err := runGit(root, opts, "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err != nil {
		return err
	}
	toks := nulList(st)
	for i := 0; i < len(toks); i++ {
		g.DirtyPaths++
		if len(toks[i]) > 0 && (toks[i][0] == 'R' || toks[i][0] == 'C') {
			i++ // a rename or copy carries its origin as the next record
		}
	}
	g.Clean = g.DirtyPaths == 0
	a.Git = g

	tracked, err := runGit(root, opts, "ls-files", "-z")
	if err != nil {
		return err
	}
	a.Tree = digestList(root, nulList(tracked))
	others, err := runGit(root, opts, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return err
	}
	u := digestList(root, nulList(others))
	a.Untracked = &u
	return nil
}
