# Release Notes Style

Reference: `v0.8.20-rc-2` and `v0.8.20-rc-3` releases (`gh release view <tag> -R EarthBuild/earthbuild --json body --jq .body`).
Write curated notes; never ship GitHub auto-generated "What's Changed" list.

## Sources
- Commits: `git log --oneline <base>..<sha>`; read each PR body (`gh pr view <n> --json title,body,author`).
- User-facing wording: `CHANGELOG.md` additions (`git diff --no-ext-diff <base> <sha> -- CHANGELOG.md`).
- Versions: `git diff --no-ext-diff <base> <sha> -- go.mod buildkitd/Earthfile '*Earthfile'` and Renovate PR tables.
- Fork pin: report `replace github.com/moby/buildkit => github.com/EarthBuild/buildkit` and `BUILDKIT_BASE_IMAGE` SHA (12 chars). `require github.com/moby/buildkit` bump has no effect -> label "overridden by fork `replace`".
- Contributors: human PR authors in `<base>..<sha>` (exclude bots like `renovate[bot]`); verify first-time contributors against history before `<base>`.

## Layout (omit empty sections; separate sections with `---`)
1. RC opener: `> Everything in <prev-rc>, plus the changes below.`, linking `<prev-rc>` to its release page (`https://github.com/EarthBuild/earthbuild/releases/tag/<prev-rc>`). Final release: no opener; cover everything since last stable.
2. `### 🚀 Improvements & Enhancements`
3. `### 🐛 Bug Fixes`
4. `### ⚠️ Breaking Changes & Deprecations`
5. `### 🧹 Maintenance` (CI, lint, repo housekeeping; one line each)
6. `### 📦 Key Dependency Updates`: one `<details>` per group, first `open`:
   - `Core Go Runtime & Modules`
   - `CLI, Build & Linting Tools` (Go tools, GitHub Actions)
   - `Base & Test Container Images`
   - `Languages & Example Ecosystems` (mark `(security)` bumps)
   - Table: `| Dependency | Previous Version | Updated Version | PR |`; one row per package, first-to-last version, list every PR that moved it.
7. `### 🙏 Contributors`: thank human contributors by GitHub `@handle` (exclude bots); call out first-time contributors with their PR link if any.
8. `**Full Changelog:**` line linking `<base>...<tag>` to `https://github.com/EarthBuild/earthbuild/compare/<base>...<tag>`.

## Bullet Style
- `* **Short Title:** What changed for users, then why (#PR, fixes #issue).`
- Link every `#PR` (`https://github.com/EarthBuild/earthbuild/pull/<n>`) and `#issue` (`https://github.com/EarthBuild/earthbuild/issues/<n>`) as Markdown links.
- Say `earth` / EarthBuild, not `earthly` / Earthly.
- Behaviour that newly fails builds (for example a previously ignored directive now enforced) -> Breaking, even when the PR is a fix.
- State only facts verified from PRs, `CHANGELOG.md` or diffs; no guessed regression versions.
- Omit issues introduced and fixed within `<base>..<sha>` (for example regressions introduced in an earlier RC of the same release).
