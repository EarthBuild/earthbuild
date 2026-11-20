---
name: earth-release
description: Prepares `earth` RC or final releases. Waits for `main` staging publish, drafts notes, creates draft GitHub release.
argument-hint: "[vX.Y.Z-rc-N | vX.Y.Z]"
user-invocable: true
---

## ROE
- **Scope**: `READ_ONLY` repo; writes draft release, scratch notes.
- **Forbidden**: Publishing (`--draft=false`), editing published releases, pushing tags without user approval.
- **Target**: `main` HEAD SHA; never tag before staging publish completes.

## Protocol
1. **Base**: RC -> previous RC (`rc-0` -> last stable). Final -> last stable.
2. **Watch** target SHA push runs, parallel with step 3:
   - `CI` step `Build and push +ci-release using latest earth build`.
   - `staging release` run (`ci-staging-deploy.yml`) -> whole run `completed success`; `release dockerhub (main)` ~30 min.
   - Failure -> stop, report.
3. **Notes** per `.agents/skills/earth-release/references/release-notes.md`:
   - RC -> only `<prev-rc>..<sha>`.
   - Final -> merge RC notes since last stable, dedupe, collapse dependency hops; cross-check every commit in `git log <last-stable>..<sha>`.
4. **Recheck** `main` HEAD equals target; moved -> ask user.
5. **Draft**: `gh release create <tag> -R EarthBuild/earthbuild --target <sha> --draft --notes-file <file>` (+ `--prerelease` for RC).
6. Return `BLUF-SBAR`. Publishing creates tag, runs `on-tag-release.yml` (copies `ghcr.io/earthbuild/earthbuild:<sha8>` to Docker Hub, attaches `<sha8>` artifact binaries).

## Recovery
- `Tagged Release` fails `manifest unknown` / `no artifact matches` -> published before staging finished. After staging succeeds: `gh run rerun <run-id> --failed`; never recreate release.
