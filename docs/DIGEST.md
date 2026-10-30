# The tree digest

`discover` proves a scan changed nothing it read by computing one value over the target before the scan and again
after it. The two values must be equal. This page says exactly how the value is made, so you can recompute it
yourself with `find` and `shasum -a 256` and compare.

## The rule

1. Pick the files (see "Which files").
2. Make one line per entry. `<path>` and `<target>` are written *escaped* (see step 3):
   - a regular file: `<lowercase sha256 of its content>`, two spaces, `x` if the owner-execute bit is set and `-` if
     it is not, two spaces, `<path>`
   - a symbolic link: `link`, two spaces, `<path> -> <target>`, where the target is the string `readlink` prints
   - something that cannot be read: `unreadable`, two spaces, `<path>`
   - in git mode, a directory that git lists (a submodule or a nested repository): `submodule`, two spaces, `<path>`
3. Paths are relative to the target root and use forward slashes. No leading `./`. A path or a link target is
   escaped before it goes into a line: a backslash becomes `\\`, a newline becomes `\n` (a backslash and an `n`), and
   `>` becomes `\>`. No name can therefore end a line early or forge the ` -> ` that separates a link from its
   target. This makes the encoding of a name injective. It does not make the digest injective over trees: an
   `unreadable` line carries only the path (see "Not covered").
4. Sort the lines bytewise (`LC_ALL=C sort`).
5. Write every line followed by one newline, and hash the whole with SHA-256. The digest is the lowercase hex.

A symbolic link is never followed: its target's content is not read and is not part of the digest. A file that cannot
be read is a line, never a gap, and the scan counts it. An empty set of lines hashes to the SHA-256 of the empty
input.

## What the digest covers, and what it does not

Covered: the names and the content of the files, the targets of the links, and the owner-execute bit of each file.

Not covered:

- **Empty directories, and directories as such.** The digest is content-addressed, as git is: a directory with no
  file under it is not part of it. Creating or removing one gives EQUAL.
- **Permission bits other than the owner-execute bit, owners, and times.** A file that loses its read permission shows
  as `unreadable`. A scan can update an access time on some filesystems; that is the operating system's doing and no
  part of the digest.
- **In git mode, ignored paths and the inside of `.git/`.** See "Which files".
- **What an unreadable entry was.** An `unreadable` line holds the path and nothing else. An unreadable directory and
  an unreadable file of the same name give the same line, and so do two unreadable files of different content: a
  change from one to the other gives EQUAL. The count of unreadable entries is in `ATTESTATION.json`.
- **In a plain directory, a FIFO, a socket or a device file.** It is not a line. One created or removed between the
  two passes gives EQUAL.
- **In git mode, the meaning of `assume-unchanged` and `skip-worktree`.** The dirty count does not read these index
  flags. It hashes the file itself, so a changed path that git has been told to ignore is counted although
  `git status` shows nothing (measured with git 2.53). The count is therefore not `git status`'s count.

A file name that holds a newline is digested by `discover`, and the shell recipes below cannot reproduce it (they read
one name per line). Every other name, including a space, a quote, a backslash and non-ASCII text, they reproduce.

## Which files

- **Plain directory** (no `.git` at its root): every regular file and every symbolic link under the root. An
  unreadable directory is one `unreadable` line for the directory.
- **Git repository** (a `.git` entry at the root): the files `git ls-files` lists. This is the *tracked* digest.
  A tracked path that is missing from the working tree is `unreadable`.
  Files that are untracked and not ignored (`git ls-files --others --exclude-standard`) get a second digest, the
  *untracked* digest, reported separately. Ignored files are in neither.
  A `.git` file at the root that points at another git directory (a linked worktree or a submodule) is read as git
  reads it: the HEAD and the branch reported are the ones the pointer leads to, not those of a repository above.

`discover` compares both digests at the end of a scan. In git mode it also compares the HEAD commit, the branch, and
the number of dirty paths.

**The git-mode proof covers** tracked and untracked-but-not-ignored content, their modes, HEAD, the branch and the
dirty count. **It does not cover ignored paths or the inside of `.git/`** (the index, hooks, configuration, refs): a
file written there between the two passes gives EQUAL. The scan prints this scope on the proof line.

**The dirty count.** A path is dirty when it differs between HEAD and the index, between the index and the working
tree, or is untracked and not ignored (an untracked directory counts once). `discover` never asks git to compare the
working tree: that would make git run the programs the repository's own configuration names (a clean filter in
`.git/config` or in a submodule's configuration). It hashes the files itself, on their raw bytes, and compares them to
the index. A repository that relies on a filter (line-ending conversion, large-file storage) can therefore show more
dirty paths than `git status` does. A rename is two paths (the old name removed, the new one added), and a copy is one
(the new name added).

## Recipes

Set `ROOT` to the target. Each recipe prints the digest. Run it with `sh`. It needs `find`, `shasum`, `sort`, `cut`,
`sed`, `tr` and `readlink`. Git mode also needs `git`, run with `GIT_OPTIONAL_LOCKS=0` so it does not refresh the
index.

Plain directory:

```sh recipe-dir
esc() { printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/>/\\>/g'; }
cd "$ROOT" && find . -mindepth 1 \( -type f -o -type l -o -type d \) -print 2>/dev/null | sed 's|^\./||' |
while IFS= read -r p; do
  if [ -L "$p" ]; then
    printf 'link  %s -> %s\n' "$(esc "$p")" "$(esc "$(readlink -- "$p")")"
  elif [ -d "$p" ]; then
    if [ ! -r "$p" ] || [ ! -x "$p" ]; then printf 'unreadable  %s\n' "$(esc "$p")"; fi
  elif [ -r "$p" ]; then
    m=-; [ -n "$(find "./$p" -prune -perm -100)" ] && m=x
    printf '%s  %s  %s\n' "$(shasum -a 256 < "$p" | cut -d' ' -f1)" "$m" "$(esc "$p")"
  else
    printf 'unreadable  %s\n' "$(esc "$p")"
  fi
done | LC_ALL=C sort | shasum -a 256 | cut -d' ' -f1
```

Git repository, tracked files:

```sh recipe-git
esc() { printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/>/\\>/g'; }
cd "$ROOT" && GIT_OPTIONAL_LOCKS=0 git ls-files -z | tr '\0' '\n' | sort -u | sed 's|/$||' |
while IFS= read -r p; do
  if [ -L "$p" ]; then
    printf 'link  %s -> %s\n' "$(esc "$p")" "$(esc "$(readlink -- "$p")")"
  elif [ -d "$p" ]; then
    printf 'submodule  %s\n' "$(esc "$p")"
  elif [ -f "$p" ] && [ -r "$p" ]; then
    m=-; [ -n "$(find "./$p" -prune -perm -100)" ] && m=x
    printf '%s  %s  %s\n' "$(shasum -a 256 < "$p" | cut -d' ' -f1)" "$m" "$(esc "$p")"
  else
    printf 'unreadable  %s\n' "$(esc "$p")"
  fi
done | LC_ALL=C sort | shasum -a 256 | cut -d' ' -f1
```

Git repository, untracked files that are not ignored:

```sh recipe-untracked
esc() { printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/>/\\>/g'; }
cd "$ROOT" && GIT_OPTIONAL_LOCKS=0 git ls-files -z --others --exclude-standard | tr '\0' '\n' | sort -u |
sed 's|/$||' |
while IFS= read -r p; do
  if [ -L "$p" ]; then
    printf 'link  %s -> %s\n' "$(esc "$p")" "$(esc "$(readlink -- "$p")")"
  elif [ -d "$p" ]; then
    printf 'submodule  %s\n' "$(esc "$p")"
  elif [ -f "$p" ] && [ -r "$p" ]; then
    m=-; [ -n "$(find "./$p" -prune -perm -100)" ] && m=x
    printf '%s  %s  %s\n' "$(shasum -a 256 < "$p" | cut -d' ' -f1)" "$m" "$(esc "$p")"
  else
    printf 'unreadable  %s\n' "$(esc "$p")"
  fi
done | LC_ALL=C sort | shasum -a 256 | cut -d' ' -f1
```

The test suite runs these three blocks, exactly as written here, over fixture trees and checks that they print the
same value as `discover`.

## The read manifest

`READ_MANIFEST.json` lists the files that an *analyser* read and cites, each with its size, modification time and
SHA-256 at the moment it was read, so a citation stays checkable after the target moves. The attestation's own reads
(the two digest passes) are not in it: the tree digest covers them. A scan that ran no analyser therefore writes an
empty manifest.

## Reading the proof

`discover scan` prints, per target, in git mode:

```
read-only proof: EQUAL <digest> (covers tracked and untracked files, modes, HEAD; not ignored paths or .git internals)
```

and in a plain directory:

```
read-only proof: EQUAL <digest> (covers files, symbolic links and the owner-execute bit; not empty directories)
```

when nothing the proof covers differs between the two passes, and

```
read-only proof: NOT-EQUAL <before> <after>
```

when the tree digest differs. A difference in another part of the proof prints its own line with the part named after
the two values: `(untracked)` for the untracked digest, `(head)`, `(branch)`, `(detached)`, `(dirty-paths)` or
`(mode)`. The exit code is 3 on any one of these differences. `ATTESTATION.json` carries the same scope text in
`read_only_proof.scope`.

What the proof does not say: it compares at two moments. A file changed and changed back between the moments gives
EQUAL, and so does anything in the "Not covered" lists above. The scan itself opens files read-only through a handle
confined to the target, never changes a mode or a modification time, never asks git to compare the working tree, and
runs git with optional locks off and a deadline (`--git-timeout`, 60 seconds by default) on every call.

Git is run in a fixed environment. Every `GIT_*` variable of the caller is removed (a `GIT_DIR` set by a git hook
must not make the scan read another repository), git is told not to fetch missing objects (`GIT_NO_LAZY_FETCH=1`, so
a partial clone's remote, which can name a program, is never contacted), and git is not allowed to look above the
target for a repository. Git older than 2.45 does not honour the no-fetch switch, so git mode refuses it by name
instead of reading without the guarantee. Several targets are written to `<out>/<label>`; a scan whose `<out>/<label>`
is a target or lies inside one is refused before anything is read.

`discover` never writes into an existing file and never writes through a link. With several targets, if a per-target
output directory already exists and is a link (or is not a directory), the scan is refused by name before anything is
read, wherever the link points. If `READ_MANIFEST.json` or `ATTESTATION.json` already exists and is a link, is not a
regular file, or is a regular file with more than one name (a hard link, where the host reports link counts), the scan
is refused by name before anything is read; remove it and run again. With one target, an `--out` that is a link is
followed to its real directory, which must lie outside every target; if it does not, the scan is refused.

Output directories are created before the target is read, through a handle confined to an existing directory outside
every target; a refusal in that step names what was created, by its name relative to `--out`, and no absolute path.
The output directory is opened once, judged as opened, and written through that same handle. Each output file is
written to a fresh name in that directory (created with `O_EXCL`) and renamed into place, so no existing file is ever
opened for writing. A hard link, a link or a FIFO that appears at an output name after the checks is replaced by the
new file; the file it pointed at is left as it was. A directory that appears at an output name cannot be replaced by a
file: the scan stops with a sentence naming the file and exit 2, and leaves no temporary file behind.

One check runs only on some hosts: the test that an `--out` spelled with another letter case is refused needs a
case-insensitive file system (the macOS default) and is skipped elsewhere, so CI on Linux does not run it. The
comparison of identities it relies on is tested on every host with a symlinked parent and `.` and `..` segments.
