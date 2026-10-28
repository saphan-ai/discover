# The tree digest

`discover` proves a scan changed nothing it read by computing one value over the target before the scan and again
after it. The two values must be equal. This page says exactly how the value is made, so you can recompute it
yourself with `find` and `shasum -a 256` and compare.

## The rule

1. Pick the files (see "Which files").
2. Make one line per entry:
   - a regular file: `<lowercase sha256 of its content>`, two spaces, `<relative path>`
   - a symbolic link: `link`, two spaces, `<relative path> -> <target string, exactly as readlink prints it>`
   - something that cannot be read: `unreadable`, two spaces, `<relative path>`
   - in git mode, a directory that git lists (a submodule or a nested repository): `submodule`, two spaces,
     `<relative path>`
3. Paths are relative to the target root and use forward slashes. No leading `./`.
4. Sort the lines bytewise (`LC_ALL=C sort`).
5. Write every line followed by one newline, and hash the whole with SHA-256. The digest is the lowercase hex.

A symbolic link is never followed: its target's content is not read and is not part of the digest. A file that cannot
be read is a line, never a gap, and the scan counts it. An empty set of lines hashes to the SHA-256 of the empty
input.

Not covered: file modes, mtimes, empty directories, and directories as such. The digest says the *content and names* of
the files did not change. The read manifest (`READ_MANIFEST.json`) is where size and mtime are kept.

A file name that holds a newline or a tab is not supported by the recipes below. `discover` still digests such a name;
the shell recipe cannot reproduce it.

## Which files

- **Plain directory** (no `.git` at its root): every regular file and every symbolic link under the root. An
  unreadable directory is one `unreadable` line for the directory.
- **Git repository** (a `.git` entry at the root): the files `git ls-files` lists. This is the *tracked* digest.
  A tracked path that is missing from the working tree is `unreadable`.
  Files that are untracked and not ignored (`git ls-files --others --exclude-standard`) get a second digest, the
  *untracked* digest, reported separately. Ignored files are in neither.

`discover` compares both digests at the end of a scan.

## Recipes

Set `ROOT` to the target. Each recipe prints the digest. Run it with `sh`. It needs `find`, `shasum`, `sort`, `cut`,
`sed` and `readlink`. Git mode also needs `git`, run with `GIT_OPTIONAL_LOCKS=0` so it does not refresh the index.

Plain directory:

```sh recipe-dir
cd "$ROOT" && find . -mindepth 1 \( -type f -o -type l -o -type d \) -print 2>/dev/null | sed 's|^\./||' |
while IFS= read -r p; do
  if [ -L "$p" ]; then
    printf 'link  %s -> %s\n' "$p" "$(readlink "$p")"
  elif [ -d "$p" ]; then
    if [ ! -r "$p" ] || [ ! -x "$p" ]; then printf 'unreadable  %s\n' "$p"; fi
  elif [ -r "$p" ]; then
    printf '%s  %s\n' "$(shasum -a 256 < "$p" | cut -d' ' -f1)" "$p"
  else
    printf 'unreadable  %s\n' "$p"
  fi
done | LC_ALL=C sort | shasum -a 256 | cut -d' ' -f1
```

Git repository, tracked files:

```sh recipe-git
cd "$ROOT" && GIT_OPTIONAL_LOCKS=0 git -c core.quotepath=off ls-files | sort -u | sed 's|/$||' |
while IFS= read -r p; do
  if [ -L "$p" ]; then
    printf 'link  %s -> %s\n' "$p" "$(readlink "$p")"
  elif [ -d "$p" ]; then
    printf 'submodule  %s\n' "$p"
  elif [ -f "$p" ] && [ -r "$p" ]; then
    printf '%s  %s\n' "$(shasum -a 256 < "$p" | cut -d' ' -f1)" "$p"
  else
    printf 'unreadable  %s\n' "$p"
  fi
done | LC_ALL=C sort | shasum -a 256 | cut -d' ' -f1
```

Git repository, untracked files that are not ignored:

```sh recipe-untracked
cd "$ROOT" && GIT_OPTIONAL_LOCKS=0 git -c core.quotepath=off ls-files --others --exclude-standard | sort -u |
sed 's|/$||' |
while IFS= read -r p; do
  if [ -L "$p" ]; then
    printf 'link  %s -> %s\n' "$p" "$(readlink "$p")"
  elif [ -d "$p" ]; then
    printf 'submodule  %s\n' "$p"
  elif [ -f "$p" ] && [ -r "$p" ]; then
    printf '%s  %s\n' "$(shasum -a 256 < "$p" | cut -d' ' -f1)" "$p"
  else
    printf 'unreadable  %s\n' "$p"
  fi
done | LC_ALL=C sort | shasum -a 256 | cut -d' ' -f1
```

The test suite runs these three blocks, exactly as written here, over fixture trees and checks that they print the
same value as `discover`.

## Reading the proof

`discover scan` prints, per target:

```
read-only proof: EQUAL <digest>
```

when the digest taken after the scan equals the one taken before it, and

```
read-only proof: NOT-EQUAL <before> <after>
```

with exit code 3 when it does not. In git mode a changed untracked digest prints its own line with `(untracked)`
after the two values.

What the proof does not say: it compares content and names at two moments. A file changed and changed back between
the moments gives EQUAL. Reading a file can update its access time on some filesystems; that is the operating system's
doing, not a change of content, and no part of the digest covers it. The scan itself opens files read-only, never
changes a mode or a modification time, and runs git with optional locks off.
