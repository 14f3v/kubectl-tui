---
name: git-workflow
description: Use when committing, branching, opening or merging a PR, or cutting a release in the kubetui (kubectl-tui) repo. Codifies this project's branch → verify → commit → PR → merge → release flow, the required pre-commit checks, the commit-script conventions, and the release process.
---

# kubetui git workflow

The canonical way to land changes in the kubectl-tui repo. Follow it for every
change that touches tracked files. It exists because CI gates `main` on gofmt +
vet + race tests + a goreleaser snapshot build, and direct pushes to `main` are
blocked.

## Golden rules

1. **Never commit or push to `main` directly.** Always work on a feature branch
   and land via a PR. Direct-to-main is classifier-gated and will be denied.
2. **Every commit must build on its own.** Cumulative per-issue commits, each of
   which compiles — verified by the cumulative-build check below.
3. **Verify before committing.** `gofmt`, `go build`, `go vet`, `go test ./...`
   all green. The CI `fmt` job fails on any un-gofmt'd file, so gofmt is not
   optional — it has bitten this repo before (hand-aligned struct/table comments
   diverge from gofmt).
4. **The user merges.** Open the PR and stop; only merge when the user says so
   ("merge #N" / "merge for me"). After a merge, sync `main` and re-verify.

## Environment

Prepend to every Go/tooling command (Homebrew Go, pinned toolchain):

```bash
export PATH="/opt/homebrew/bin:$PATH"; export GOTOOLCHAIN=local
```

## Step 1 — branch

Branch off an up-to-date `main`. Name by intent: `feat/…`, `fix/…`, `ci/…`,
`sprint/…`.

```bash
git checkout -q main && git pull -q origin main
git checkout -q -b feat/<short-name>
```

If you are building on work that is still in an **unmerged** PR, base the new
branch on that branch instead of `main` (stacked PR). Note: when the base branch
is deleted on merge, GitHub may **close** the stacked PR rather than retarget it —
if that happens, reopen it against `main` (the commits are intact; confirm they
still apply cleanly with `git merge-base` + `git merge-tree`).

## Step 2 — verify

```bash
gofmt -w <files-you-touched>            # or: gofmt -l internal cmd hack  (must be empty)
go build ./... && go vet ./... && go test ./... 2>&1 | grep -E "FAIL" || echo GREEN
```

For substantial or risky logic, add unit tests and consider an adversarial review
pass before committing.

## Step 3 — commit (per-issue, cumulative, each builds)

Split a change into commits that each build. Use a **bash** commit script — zsh
does not word-split an unquoted `$1`, so a script that passes file lists as one
argument must run under `bash`, not the default zsh.

Commit-message trailers are required. End every commit message with the
`Co-Authored-By:` and `Claude-Session:` lines **exactly as given in the current
session's instructions** (the model name and session URL are session-specific — do
not hardcode a stale URL from this file).

Template (`commit.sh`, run with `bash commit.sh`):

```bash
#!/usr/bin/env bash
set -euo pipefail
export PATH="/opt/homebrew/bin:$PATH"
cd "$(git rev-parse --show-toplevel)"
# TRAILER must match the current session's required trailers verbatim:
TRAILER=$'\nCo-Authored-By: <model> <noreply@anthropic.com>\nClaude-Session: <session-url>'
commit() { git add $1; printf '%s%s\n' "$2" "$TRAILER" | git commit -q -F -; git log --oneline -1; }

commit "path/to/a.go path/to/a_test.go" \
"Short imperative subject

Body: what and why. Reference issues with 'Closes #N' (or 'Refs #N' when a later
commit completes it)."
```

Cluster files by issue. When a shared file (e.g. `internal/view/resource.go`)
spans several issues, land its edits in one final "wire it together" commit that
`Closes` those issues, and keep the earlier commits to isolated new
files/packages (unused new funcs/types compile fine, so they can precede the
commit that calls them).

## Step 4 — cumulative build check

Confirm each commit builds independently before pushing:

```bash
for sha in $(git rev-list --reverse main..HEAD); do
  git checkout -q "$sha"
  go build ./... >/dev/null 2>&1 && s=OK || s=FAIL
  printf '%s  %s  %s\n' "${sha:0:9}" "$s" "$(git log -1 --format=%s $sha)"
done
git checkout -q <your-branch>
```

## Step 5 — push + PR

```bash
git push -u origin <your-branch>
gh pr create --base main --head <your-branch> --title "<title>" --body-file /tmp/pr_body.md
```

PR body: lead with the problem, then what changed and how it's verified; end the
body with the Claude Code footer line the session requires. Title is a concise
imperative summary. Then **report the PR number to the user and wait** — do not
merge.

## Step 6 — merge (only on the user's word) + sync

```bash
gh pr merge <N> --merge --delete-branch
git checkout -q main && git pull -q origin main
go build ./... && go test ./... 2>&1 | grep -E "FAIL" || echo GREEN
```

`--merge` (merge commit) is the house style here. After merging, always sync
`main` and re-verify green.

## Releases

Releases are tag-driven. `.github/workflows/release.yml` runs
`goreleaser release --clean` on any `v*` tag and publishes a GitHub Release with
binaries (linux/darwin × amd64/arm64), archives, checksums, and a changelog.
`.goreleaser.yaml` marks pre-release tags (e.g. `v1.0.0-rc1`) as prereleases.

Cut a release from a green `main`:

```bash
git checkout main && git pull
git tag v0.1.0            # annotated is fine: git tag -a v0.1.0 -m v0.1.0
git push origin v0.1.0
```

Watch it: `gh run watch $(gh run list --workflow release.yml -L1 --json databaseId -q '.[0].databaseId') --exit-status`,
then `gh release view v0.1.0`.

Setup note: the release job needs **Settings → Actions → General → Workflow
permissions → Read and write**, or the release upload 403s. A Homebrew tap (the
commented `brews:` block in `.goreleaser.yaml`) additionally needs a PAT with
write access to a `homebrew-tap` repo, wired in as a secret.

## Gotchas learned here

- **gofmt before commit, always.** CI's `fmt` job (`gofmt -l internal cmd hack`)
  fails otherwise; hand-aligned comment columns are the usual culprit.
- **Run commit scripts under `bash`,** not zsh (word-splitting of file lists).
- **`Date.now`/random are unavailable in workflow scripts** — irrelevant to git,
  but note timestamps come from `args`, not the shell, in Workflow runs.
- **CI actions are pinned to Node-24 majors** (checkout@v5, setup-go@v6,
  goreleaser@v7); keep new/edited workflows on those to avoid deprecation warnings.
