## Releasing instructions

### earth

Releases are cut automatically by [semantic-release](https://semantic-release.gitbook.io) (configured in [`.releaserc.json`](../.releaserc.json)) from [Conventional Commits](https://www.conventionalcommits.org). There is no release tag to choose or GitHub release to create by hand.

- Use a conventional commit message (or PR title, when squash-merging) for every change. `fix:` and `perf:` cause a patch release, `feat:` a minor release, and `feat!:` or a `BREAKING CHANGE:` footer a major release. Other types (`chore:`, `docs:`, `test:`, ...) do not release by themselves.
- Every push to `main` runs the [staging release](../.github/workflows/ci-staging-deploy.yml) workflow. Once all staging artifacts and images for the commit are published, its `release` job runs semantic-release, which tags the commit `vX.Y.Z` and creates the GitHub release with generated release notes.
- For a stable release, the same workflow then calls the [Promote Release](../.github/workflows/promote-release.yml) workflow (uploads the release artifacts and promotes the images to DockerHub) and the [Merge main to docs-0.8](../.github/workflows/release-merge-docs.yml) workflow. They are called directly because tags and releases created with `GITHUB_TOKEN` do not trigger other workflows.
- Make sure the following build status are green:
  | Platform | Status |
  | ------------- | ------------- |
  | MacOS (x86) | [![Build status](https://badge.buildkite.com/cc0627732806ab3b76cf13b02c498658b851056242ec28f62d.svg)](https://buildkite.com/earthly-technologies/earthly-mac-scheduled)
  | MacOS (M1) | [![Build status](https://badge.buildkite.com/10a7331b2032fcc9f7f311c5218d12c1a18c317cd7fc9270ba.svg)](https://buildkite.com/earthly-technologies/earthly-m1-scheduled)

- If publishing package repositories (apt/yum), using the tag of the new release:
  ```bash
  export RELEASE_TAG="v..."
  earth --push ./release+release-repo --RELEASE_TAG="$RELEASE_TAG"
  ```
- Wait for the [Merge main to docs-0.8 on New EarthBuild Release](../.github/workflows/release-merge-docs.yml) workflow to complete; this workflow automatically merges `main` into `docs-0.8`. You can watch for it here: [![Merge main to docs-0.8 on New EarthBuild Release](https://github.com/earthbuild/earthbuild/actions/workflows/release-merge-docs.yml/badge.svg)](https://github.com/earthbuild/earthbuild/actions/workflows/release-merge-docs.yml)
  In case the workflow fails the manual process is:
  ````shell
    git checkout docs-0.8 && git pull && git merge main && git push
    ```
  ````
- Updating the EarthBuild version in our docs:
  [Renovate](https://www.mend.io/renovate/) will open a PR targeting `docs-0.8` branch to update all docs as soon as a new release is available in this repo which you should then review & merge (An example PR can be found [here](https://github.com/earthly/earthly/pull/3285/files)).
- Merge `docs-0.8` into `main`.
  ```shell
    git checkout main && git merge docs-0.8 && git push
  ```

- Note: If you don't have permissions to push directly to `main` branch, do the following:
- `git checkout -b soon-to-be-main && git push origin soon-to-be-main`
- Open a PR against the new branch and get it approved; IMPORTANT: don't squash-merge via github
- Once all (required) checks pass, try pushing the branch again:
      `git checkout main && git push`

<!-- vale HouseStyle.Spelling = YES -->

- Wait for the [Check Docs for Broken Links](../.github/workflows/docs-checks-links.yml) workflow to complete; this workflow validates https://docs.earthbuild.dev does not contain any broken links. You can watch for it here: [![Check Docs for Broken Links](https://github.com/earthbuild/earthbuild/actions/workflows/docs-checks-links.yml/badge.svg?event=push)](https://github.com/earthbuild/earthbuild/actions/workflows/docs-checks-links.yml)
- Verify the Homebrew formula in [earthbuild/homebrew-tap](https://github.com/earthbuild/homebrew-tap) is updated (Renovate detects the new EarthBuild release and the tap's GitHub workflows release it automatically).
- Copy the release notes you have written before and paste them in the EarthBuild Community slack channel `#announcements`, together with a link to the release's GitHub page. If you have Slack markdown editing activated, you can copy the markdown version of the text.

#### Prereleases

To publish a prerelease from a `feat/<name>` branch, run the [Prerelease](../.github/workflows/release-prerelease.yml) workflow on that branch. It publishes a GitHub prerelease tagged `vX.Y.Z-<name>.N` (`<name>` must only contain letters, digits and `-`). Run it after a `feat:`, `fix:` or `perf:` commit, otherwise there is nothing to release and the job fails. Prereleases only get a tag and release notes; binaries and images are not promoted.

### One-Time (clear this section when done during release)

- Add new one-time items here.

#### Performing a test release

To test staging releases via CI, push your changes to a branch matching `**-staging-test` (which triggers the [staging release](../.github/workflows/ci-staging-deploy.yml) workflow) or manually trigger the workflow via `workflow_dispatch`.

NOTE: apt and yum repos do not currently support test releases. (TODO: fix this)

#### Rollbacks

If you need to rollback/disable a version:

1. Go to [GitHub releases](https://github.com/earthbuild/earthbuild/releases), click on the `edit release` button, then check the `This is a prerelease` checkbox.
2. Check out the [earthbuild/homebrew-tap](https://github.com/earthbuild/homebrew-tap) repo, and run:

```bash
git checkout main
git revert --no-commit 123abc..HEAD # where `123abc` is the sha1 commit to roll back to
git commit # enter a message saying you are rolling back
git push
```

3. Mark the release title in [CHANGELOG.md](../CHANGELOG.md) as `(aborted release/not recommended)`, e.g.:
   `## v0.7.18 - 2023-09-18 (aborted release/not recommended)`
4. TODO need to create targets for apt and yum Earthfiles to perform rollbacks

### dind

Docker-in-Docker (dind) images change less frequently than EarthBuild, but take a long time to build.
earthbuild/dind images and their releases are maintained in [project repo](https://github.com/earthbuild/dind).

### Syntax Highlighting Releases

We currently have syntax highlighting for the following:

1. [vscode + github](https://github.com/EarthBuild/earthfile-grammar)
1. [intellij](https://github.com/EarthBuild/earthly-intellij-plugin) (py, go, java)
1. [vim](https://github.com/EarthBuild/earthly.vim)
1. [sublime](https://github.com/earthly/sublimetext-earthly-syntax)
1. [emacs](https://github.com/EarthBuild/earthly-emacs)

#### VSCode + GitHub

Release instructions can be found in the [project repo](https://github.com/EarthBuild/earthfile-grammar#how-to-release).

#### Intellij

Intellij pulls its syntax highlighting from the [same repo used by VSCODE + GitHub](https://github.com/EarthBuild/earthfile-grammar) and so should be released after to keep up to date.

1. Go to the [repo](https://github.com/EarthBuild/earthfile-grammar)
1. Make relevant changes to the branches + test in this order:
   1. py
   1. go
   1. main
1. Sign + release the changes from each branch in this order:
   1. py
   1. go
   1. main

   Follow the instructions on how to sign and release as written in the [README](https://github.com/EarthBuild/earthly-intellij-plugin#signing-requires-earthly-technologies-org-membership)

#### Vim

1. Go to the [repo](https://github.com/EarthBuild/earthly.vim)
1. Make relevant updates and test
1. Once merged to main it will be released

#### Sublime Text

1. Go to the [repo](https://github.com/earthly/sublimetext-earthly-syntax)
1. Make relevant updates and test
1. Once merged to main it will be released

#### Emacs

1. Go to the [repo](https://github.com/EarthBuild/earthly-emacs)
1. Make relevant updates and test
1. Once merged to main it will be released
