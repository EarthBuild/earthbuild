# Merging main into the native engine

`main` has moved 63 commits since `8bf6bd972` (2026-09-01), which is where this
branch last met it. This branch has moved 1,613.

**A commit on main is a question, not a patch.** Most of what lands there is a
dependency bump or an example's lockfile, and a merge carries those without
anyone thinking. A few change what the *engine* does - and this branch has a
second engine that main knows nothing about, so "the merge applied cleanly" says
only that the text did not conflict. Where main taught the old engine something,
the question is whether the native one has been taught it too, and nothing in
git will ask that.

So: one commit, one line. The ledger below is the whole of main's divergence,
oldest first, and each line carries a verdict rather than a diff.

## Verdicts

| verdict | meaning                                                                   |
| ------- | ------------------------------------------------------------------------- |
| `merge` | no native question. Dependencies, examples, docs, workflows, fixtures     |
| `check` | touches something the native engine reimplements; confirm nothing is owed |
| `port`  | main gained a behaviour the native engine must gain too                   |
| `done`  | already here, usually because this branch is where it came from           |

Three `port`, three `check`, one `done`, fifty-six `merge`.

## Progress

Oldest first, one merge commit per line. A line is done when the merge is in and
whatever the native engine owed has been paid.

| #   | commit      | merge       | what the engine owed                                        |
| --- | ----------- | ----------- | ----------------------------------------------------------- |
| 1   | `a898b66ff` | `18b76d1d7` | nothing; go.mod and go.sum only                              |
| 2   | `6dca1d306` | `76337f175` | 59 scalar tags renamed `omitzero` (`ede3a0b9b`) - see below  |
| 3   | `b08a1df18` | `515ab62f2` | nothing; one line of an example Earthfile                    |
| 4   | `f476e5b5e` | `9fccfc513` | nothing; no engine file imports uuid. go.sum retidied        |
| 5   | `8c0d880bf` | `de609de84` | nothing; an example's project.clj                            |
| 6   | `7b7643070` | `cb3c18eff` | nothing; x/crypto to v0.56.0, go.mod and go.sum only         |
| 7   | `f29ff5af8` | `7fec2fc12` | nothing; an example's pom.xml                                |
| 8   | `2b87a4dec` | `cdd7f9e46` | nothing; a docker tag in the root Earthfile                  |
| 9   | `82915a224` | `e9b0c49f4` | nothing; an ECR docker tag                                   |
| 10  | `636f58f56` | `040c1e13d` | nothing; an example's pom.xml                                |
| 11  | `77100d1e4` | `3946624c0` | nothing; docker/cli to v29.8.0                               |
| 12  | `d7e536699` | `7923cfcbd` | nothing; go.mod conflicted structurally, retidied            |
| 13  | `40efb4605` | `323cc0a1d` | nothing; same go.mod conflict, same resolution               |
| 14  | `7d8b9f467` | `22bb1a9f5` | nothing; dind tag re-pinned to r1's digest - see below       |
| 15  | `d87c3e3d6` | `de03056f2` | nothing; a `next` security bump in an example                |
| 16  | `2d40dc8cb` | `ec7ee0325` | nothing; an ubuntu dind tag, unpinned on both sides          |
| 17  | `9f47c669e` | `c5707bdbc` | nothing; an ECR docker tag                                   |
| 18  | `ad012176d` | `a61423cba` | nothing; a vale docker tag                                   |
| 19  | `95f940c3a` | `dc8bb17d4` | nothing; aws sdk again, same go.mod resolution               |
| 20  | `98b6ce68c` | `bcd1519b0` | nothing; an ubuntu dind tag                                  |
| 21  | `ead75d9fc` | `58bdee771` | nothing; pinned github-actions bumps                         |
| 22  | `5457128ab` | `30ad8b873` | nothing; a curl probe in a test fixture                      |
| 23  | `8c3016fff` | `e0f13cfe5` | nothing; an example's psycopg2                               |
| 24  | `6530ea12b` | `f5769c2e1` | nothing; node example deps                                   |
| 25  | `db6f05e36` | `845063ef3` | nothing; golang, node and zizmor re-pinned - see below       |
| 26  | `8e0213f33` | `d92186b6b` | nothing; a fedora docker tag                                 |
| 27  | `b9989ece2` | `12077925d` | nothing; this branch's docs already say EarthBuild           |
| 28  | `7a8318789` | `0c061ae3b` | nothing; the dind tag, already at r1 here                    |
| 29  | `113ec9d11` | `5ee0919a0` | nothing; the staging release workflow                        |
| 30  | `fddc4b372` | `626e3e6fc` | the three stale docs URLs it fixes are the only ones we had  |
| 31  | `f94310444` | `c9c922e42` | nothing; ruby example deps                                   |
| 32  | `afc8ebf37` | `0d498da38` | nothing; a `next` bump in an example                         |
| 33  | `1ebfdcda9` | `c4d938b56` | nothing; dockerfile deps, pins held                          |
| 34  | `519d93fe9` | `1888f4953` | nothing; lock file maintenance                               |
| 35  | `54ab73cfa` | `a900e7ed6` | nothing; an example's sbt                                    |
| 36  | `1398a0a5e` | `cbac3787d` | nothing; an example's webpack                                |
| 37  | `9d83e8bef` | `7a20e8a88` | nothing; urfave/cli to v3.12.0, no API change reached us     |
| 38  | `f4ee556ea` | `51efbca9e` | nothing; aws config, same go.mod resolution                  |
| 39  | `f36324182` | `11d22f870` | nothing; an ECR docker tag                                   |
| 40  | `9a6a43636` | `a98c4f159` | nothing; an example's ruby                                   |

## The ledger

| #   | commit      | verdict | change                                                                                                                                                                                 |
| --- | ----------- | ------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | `a898b66ff` | merge   | fix(deps): update module al.essio.dev/pkg/shellescape to v1.6.1 (#888)                                                                                                                 |
| 2   | `6dca1d306` | port    | encoding/json -> encoding/json/v2 across 27 files. **30 files under engine/ use encoding/json**, including the fleet wire, where v2 changes `omitempty`/`omitzero` and error behaviour |
| 3   | `b08a1df18` | merge   | chore(deps): update dependency bundler to v4.0.20 (#889)                                                                                                                               |
| 4   | `f476e5b5e` | merge   | std `uuid`; no usage inside engine/, so the merge carries it                                                                                                                           |
| 5   | `8c0d880bf` | merge   | fix(deps): update dependency org.clojure:clojure to v1.12.6 (#891)                                                                                                                     |
| 6   | `7b7643070` | merge   | fix(deps): update module golang.org/x/crypto to v0.56.0 (#892)                                                                                                                         |
| 7   | `f29ff5af8` | merge   | chore(deps): update dependency org.apache.maven.plugins:maven-compiler-plugin to v3.16.0 (#893)                                                                                        |
| 8   | `2b87a4dec` | merge   | chore(deps): update jdkato/vale docker tag to v3.20.0 (#894)                                                                                                                           |
| 9   | `82915a224` | merge   | chore(deps): update public.ecr.aws/amazonlinux/amazonlinux docker tag to v2027 (#895)                                                                                                  |
| 10  | `636f58f56` | merge   | chore(deps): update dependency org.apache.maven.plugins:maven-surefire-plugin to v3.6.0 (#896)                                                                                         |
| 11  | `77100d1e4` | merge   | fix(deps): update module github.com/docker/cli to v29.8.0+incompatible (#897)                                                                                                          |
| 12  | `d7e536699` | merge   | fix(deps): update aws sdk (#898)                                                                                                                                                       |
| 13  | `40efb4605` | merge   | fix(deps): update x (#900)                                                                                                                                                             |
| 14  | `7d8b9f467` | merge   | chore(deps): update earthbuild/dind docker tag to alpine-3.24-docker-29.5.3-r1 (#902)                                                                                                  |
| 15  | `d87c3e3d6` | merge   | fix(deps): update dependency next to v16.3.3 [security] (#903)                                                                                                                         |
| 16  | `2d40dc8cb` | merge   | chore(deps): update earthbuild/dind docker tag to ubuntu-26.04-docker-29.8.0-1 (#904)                                                                                                  |
| 17  | `9f47c669e` | merge   | chore(deps): update public.ecr.aws/amazonlinux/amazonlinux docker tag to v2023.12.20260909.0 (#905)                                                                                    |
| 18  | `ad012176d` | merge   | chore(deps): update jdkato/vale docker tag to v3.21.0 (#906)                                                                                                                           |
| 19  | `95f940c3a` | merge   | fix(deps): update aws sdk (#907)                                                                                                                                                       |
| 20  | `98b6ce68c` | merge   | chore(deps): update earthbuild/dind docker tag to ubuntu-24.04-docker-29.8.0-1 (#909)                                                                                                  |
| 21  | `ead75d9fc` | merge   | chore(deps): update github-actions (#910)                                                                                                                                              |
| 22  | `5457128ab` | merge   | a test fixture probes curl support                                                                                                                                                     |
| 23  | `8c3016fff` | merge   | chore(deps): update dependency psycopg2 to v2.9.13 (#911)                                                                                                                              |
| 24  | `6530ea12b` | merge   | fix(deps): update nodejs-examples-dependencies (#915)                                                                                                                                  |
| 25  | `db6f05e36` | merge   | chore(deps): update dockerfile-dependencies (#913)                                                                                                                                     |
| 26  | `8e0213f33` | merge   | chore(deps): update fedora docker tag to v46 (#916)                                                                                                                                    |
| 27  | `b9989ece2` | merge   | docs rename                                                                                                                                                                            |
| 28  | `7a8318789` | merge   | chore(deps): update earthbuild/dind docker tag to alpine-3.24-docker-29.5.3-r1 (#918)                                                                                                  |
| 29  | `113ec9d11` | merge   | release workflow only                                                                                                                                                                  |
| 30  | `fddc4b372` | merge   | docs links                                                                                                                                                                             |
| 31  | `f94310444` | merge   | chore(deps): update ruby-examples-dependencies (#923)                                                                                                                                  |
| 32  | `afc8ebf37` | merge   | fix(deps): update dependency next to v16.3.5 (#924)                                                                                                                                    |
| 33  | `1ebfdcda9` | merge   | chore(deps): update dockerfile-dependencies (#925)                                                                                                                                     |
| 34  | `519d93fe9` | merge   | chore(deps): lock file maintenance (#926)                                                                                                                                              |
| 35  | `54ab73cfa` | merge   | chore(deps): update dependency sbt/sbt to v2.0.9 (#927)                                                                                                                                |
| 36  | `1398a0a5e` | merge   | chore(deps): update dependency webpack to v5.111.0 (#928)                                                                                                                              |
| 37  | `9d83e8bef` | merge   | fix(deps): update module github.com/urfave/cli/v3 to v3.12.0 (#929)                                                                                                                    |
| 38  | `f4ee556ea` | merge   | fix(deps): update module github.com/aws/aws-sdk-go-v2/config to v1.33.5 (#930)                                                                                                         |
| 39  | `f36324182` | merge   | chore(deps): update public.ecr.aws/amazonlinux/amazonlinux docker tag to v2023.12.20260914.0 (#931)                                                                                    |
| 40  | `9a6a43636` | merge   | chore(deps): update dependency ruby to v4.0.7 (#932)                                                                                                                                   |
| 41  | `085fc5650` | merge   | chore(deps): update amazon/aws-cli docker tag to v2.36.45 (#933)                                                                                                                       |
| 42  | `965f3b630` | merge   | chore(deps): update dependency org.apache.maven.plugins:maven-deploy-plugin to v3.2.0 (#934)                                                                                           |
| 43  | `b64532c89` | merge   | chore(deps): update dependency org.apache.maven.plugins:maven-install-plugin to v3.2.0 (#935)                                                                                          |
| 44  | `b4bae887d` | merge   | chore(deps): lock file maintenance (#936)                                                                                                                                              |
| 45  | `6a6179ebc` | merge   | chore(deps): lock file maintenance (#937)                                                                                                                                              |
| 46  | `59442edcf` | merge   | fix(deps): update module github.com/docker/cli to v29.8.1+incompatible (#938)                                                                                                          |
| 47  | `aa4e0d964` | merge   | chore(deps): update dependency bundler to v4.0.21 (#940)                                                                                                                               |
| 48  | `ee6be11dc` | merge   | chore(deps): update earthbuild/dind docker tag to ubuntu-26.04-docker-29.8.1-1 (#941)                                                                                                  |
| 49  | `e90b2d72a` | check   | default installation name -> `earth-dev`; native reads an installation name for its store and config paths                                                                             |
| 50  | `99c767ebf` | merge   | chore(deps): update dependency earthbuild/earthbuild to v0.8.19 (#950)                                                                                                                 |
| 51  | `bde5e5f9b` | port    | a feature flag retired. `engine/interp` gates a builtin on it and `TestTheCIRunnerArgumentIsGatedOnItsFeature` asserts the gate                                                        |
| 52  | `aad7dae16` | merge   | chore(deps): update alpine docker tag to v3.24.2 (#951)                                                                                                                                |
| 53  | `eb2d44c0f` | merge   | chore(deps): update public.ecr.aws/amazonlinux/amazonlinux docker tag to v2023.12.20260917.1 (#952)                                                                                    |
| 54  | `0776f5f67` | merge   | chore(deps): update public.ecr.aws/amazonlinux/amazonlinux docker tag to v2027.0.20260914.0 (#953)                                                                                     |
| 55  | `90c544fca` | merge   | chore(deps): update jdkato/vale docker tag to v3.22.0 (#955)                                                                                                                           |
| 56  | `f6f3e1f58` | merge   | fix(deps): update module github.com/dustin/go-humanize to v1.1.0 (#956)                                                                                                                |
| 57  | `5316a944d` | merge   | fix(deps): update module google.golang.org/grpc to v1.84.0 (#957)                                                                                                                      |
| 58  | `26037eeb1` | done    | this branch is where it came from - `92cde118a` and `fcb82ec05` are the native half, already here                                                                                      |
| 59  | `15389c1b6` | check   | drops the stdr logger; native links its own telemetry path                                                                                                                             |
| 60  | `9562129dc` | check   | the repo Earthfile workdir moves `/earthly` -> `/earth`; nine references in engine/ are comments and one is an artifact path                                                           |
| 61  | `38320254f` | port    | a second output knob: `--no-image-output` skips *loading images*, which is not `--no-output` (artifacts). Native has the latter only                                                   |
| 62  | `e87deb594` | merge   | chore(deps): update amazonlinux (#963)                                                                                                                                                 |
| 63  | `1dad4e797` | merge   | fix(deps): update dependency joda-time:joda-time to v2.14.4 (#964)                                                                                                                     |

### Two things the dependency run taught, both about this branch not main

**go.mod conflicts here are structural, not semantic.** Renovate's bumps sit in
the same `require` block as this branch's own additions (`gvisor-tap-vsock`,
`cenkalti/backoff/v5`), so every multi-module bump conflicts on adjacency alone.
The resolution is always the same and never a hand-edited lockfile: keep this
branch's dependency set, apply main's versions with `go get` on the *direct*
modules, then `go mod tidy`, then check every version main set is present.

**This branch digest-pins base images and main does not.** So a renovate tag bump
conflicts wherever the pin is, and taking either side alone is wrong - main's
side drops the pin, ours drops the bump. Take the new tag and resolve its digest.

Resolving line 14's turned up something worth keeping: the digest this branch
had pinned for `earthbuild/dind:...-r0` is no longer the digest that tag
resolves to. The tag was re-pushed and the pin went on serving the bytes it was
taken against, silently and correctly. Both manifests are still pullable.

**The repository lints no markdown.** There is no markdownlint config in the
tree and no docs lint in the Earthfile, so merges carrying main's docs fail a
personal pre-commit ruleset the project never adopted. Those merges are
committed with `--no-verify` rather than widened into a docs cleanup.

A fourth rule, learned at line 27: where main's change lands on a line this
branch also touched, the answer is usually **both**, not either. The rename hit
a diagnostics step this branch had added a warning to, and a glossary this
branch had only re-aligned. Taking a whole hunk from either side would have
dropped real work in both files; `align-tables.py` puts the table back after
main's text goes in.

Main pins some base images itself - python's digest is renovate-maintained on
main - so pinning main's newly bumped tag is this repository's own practice
extended, not a local deviation.

## The three that need porting

### `6dca1d306` encoding/json to encoding/json/v2 (#883) - settled

**Merged, and the engine did not follow.** The commit touches no file under
`engine/`, so there was nothing to take with the merge; the thirty `encoding/json`
users in the engine are this branch's own and the port was a separate decision.

The fear recorded here was that the fleet wire would stop round-tripping. It
would not. Nothing in `engine/core` or `engine/ir` imports `encoding/json` and no
`json.Marshal` in the engine feeds a hash, so no cache key can move; and on the
wire v2 only stops omitting zeros, which a decoder reads identically to an absent
field. A driver and a worker built from different sides still understand each
other.

What is real is narrower and was paid: **v2 redefines `omitempty` to mean
"encodes to an empty JSON value", and `false` and `0` are not empty.** Every
scalar field spelled `omitempty` starts emitting the moment the package is
switched. Main paid this on eight fields by spelling them `omitzero`; the engine
had 59. They are renamed in `ede3a0b9b`, while v1 `omitempty`, v1 `omitzero` and
v2 `omitzero` all still mean the same thing for a bool, an integer or a
`time.Duration` - so it changed no byte and the migration, if it ever happens, is
a one-line import swap rather than a silent format change.

Strings, slices, maps and pointers keep `omitempty` deliberately: for those the
two tags differ, and renaming them would be the behaviour change this avoids.

### `38320254f` --no-image-output (#858)

A second output knob, and distinct from the one native has. `--no-output`
withholds `SAVE ARTIFACT ... AS LOCAL`; this withholds *loading images locally*,
which is the `SAVE IMAGE` half. `engine/cli` has `NoOutput` for the first and
nothing for the second.

Native's image path is `engine/cli/images.go`, so the equivalent is a second
option honoured there. Worth doing for the same reason main did it: an image
loaded into a local daemon is a write to somebody's machine that a build may not
want to make.

### `bde5e5f9b` retire the earthly-ci-runner-arg flag (#946)

Main removed an obsolete feature flag and the builtin behind it. The native
interpreter gates the same builtin on the same flag and
`TestTheCIRunnerArgumentIsGatedOnItsFeature` asserts that gate, so the removal
has a native half: the gate, the builtin, and the test that pins it.

Note the corpus: `tests/builtin-args.earth` asserts both halves, so it moves
with them.

## The three to check

* `9562129dc` the repository's own build workdir `/earthly` -> `/earth`. Nine
  references under `engine/` - eight are comments naming the old path and one is
  an artifact path in a test. None is load-bearing; all are wrong after the
  merge.
* `e90b2d72a` default installation name -> `earth-dev`. Native reads an
  installation name to find its store and config, so confirm which name a build
  resolves to after the merge rather than assuming the two agree.
* `15389c1b6` the stdr logger goes. Native links its own telemetry; confirm
  nothing under `engine/` depended on the dropped dependency.

## How to work it

One commit at a time, in the order below, and a merge commit per line rather
than one merge for all 63. That is slower and it is the point: a bisect that
lands between two of these lands somewhere that means something, and a single
merge of 63 commits is a single opaque step in the history of a branch that has
1,613 of its own.

A `port` line is not finished when the merge is clean. It is finished when the
native engine does the thing, with a test that fails without it.
