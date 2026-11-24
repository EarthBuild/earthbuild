# Release Notes Style

Reference: `v0.8.20-rc-2` and `v0.8.20-rc-3` releases (`gh release view <tag> -R EarthBuild/earthbuild --json body --jq .body`).
Write curated notes; never ship GitHub auto-generated "What's Changed" list.

## Sources
- Commits: `git log --oneline <base>..<sha>`; read each PR body (`gh pr view <n> --json title,body,author`).
- User-facing wording: `CHANGELOG.md` additions (`git diff --no-ext-diff <base> <sha> -- CHANGELOG.md`).
- Versions: `git diff --no-ext-diff <base> <sha> -- go.mod buildkitd/Earthfile '*Earthfile'` and Renovate PR tables.
- Fork pin: report only `replace github.com/moby/buildkit => github.com/EarthBuild/buildkit` and `BUILDKIT_BASE_IMAGE` SHA (12 chars), and inspect fork commits (`gh api repos/EarthBuild/buildkit/compare/<old>...<new>`) for user-facing fixes. Omit `require github.com/moby/buildkit` bumps — they are overridden by the fork `replace` and have no effect.
- Contributors: human PR authors in `<base>..<sha>` (exclude bots like `renovate[bot]`); verify first-time contributors against history before `<base>`.
- Docs: docs publish automatically from `main` to `https://docs.earthbuild.dev`. Check changed pages (`git diff --name-only <base> <sha> -- docs/`), resolve live URLs from `https://docs.earthbuild.dev/sitemap-pages.xml`, and extract exact `#anchor` IDs from rendered `<h1..h4 id="...">` elements in the live HTML (GitBook paths follow `docs/SUMMARY.md` and H5 headings get no `id`; never guess URLs or anchors).

## Layout (omit empty sections; separate sections with `---`)
1. Opener:
   - RC: `> Everything in <prev-rc>, plus the changes below.`, linking `<prev-rc>` to its release page (`https://github.com/EarthBuild/earthbuild/releases/tag/<prev-rc>`).
   - Final: 2–3 sentence upgrade summary covering everything since last stable, highlighting breaking changes, cache invalidations, and any remote `buildkitd` upgrade requirements.
2. `### ⚠️ Breaking Changes & Deprecations`
3. `### 🚀 Improvements & Enhancements` (include actionable usage/config details such as requirements, flags/config, and measured wins; keep internal refactors in Maintenance)
4. `### 🐛 Bug Fixes`
5. `### 🧹 Maintenance` (internal refactors, CI, lint, repo housekeeping; one line each)
6. `### 📦 Key Dependency Updates`: one `<details>` per group, first `open`:
   - `Core Go Runtime & Modules` (include release Go toolchain)
   - `CLI, Build & Linting Tools (CI & repo tooling)` (Go tools, GitHub Actions)
   - `Base & Test Container Images (CI & tests)`
   - `Languages & Example Ecosystems (examples only — not shipped in earth or buildkitd)` (mark `(security)` bumps)
   - Table: `| Dependency | Previous Version | Updated Version | PR |`; one row per package, first-to-last version; collapse >5 Renovate bumps to `<first-PR> .. <last-PR> (<N> PRs)`.
7. `### 🙏 Contributors`: thank human contributors by GitHub `@handle` (exclude bots); call out first-time contributors with their PR link if any.
8. `**Full Changelog:**` line linking `<base>...<tag>` to `https://github.com/EarthBuild/earthbuild/compare/<base>...<tag>`.

## Bullet Style
- `* **Short Title:** What changed for users, then why (#PR, fixes #issue).`
- Link every `#PR` (`https://github.com/EarthBuild/earthbuild/pull/<n>`) and `#issue` (`https://github.com/EarthBuild/earthbuild/issues/<n>`) as Markdown links.
- Say `earth` / EarthBuild, not `earthly` / Earthly.
- Behaviour that newly fails builds (for example a previously ignored directive now enforced) -> Breaking, even when the PR is a fix.
- State only facts verified from PRs, `CHANGELOG.md` or diffs; no guessed regression versions.
- Omit issues introduced and fixed within `<base>..<sha>` (for example regressions introduced in an earlier RC of the same release).
- Link relevant published doc pages on `https://docs.earthbuild.dev` for new features, CLI flags, config options, and upgrade/migration steps. Always link to the exact `#section` anchor on multi-section/reference pages (verified against rendered `id="..."` in live HTML) rather than the top of a large page.
