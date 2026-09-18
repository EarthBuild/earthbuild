# Migrating from `earthly` to EarthBuild

[`earthly`](https://github.com/earthly/earthly) was originally developed by [Earthly
Technologies](https://earthly.dev) as a corporately-sponsored open-source project.

In 2025, earthly [announced a pivot to a different business
model](https://web.archive.org/web/20250420142821/https://earthly.dev/blog/shutting-down-earthfiles-cloud/),
no longer maintaining `earthly` to focus on entirely different products and directions.

In response, the community has forked the project under the name `EarthBuild` to continue its development and maintenance.

## What to Expect from EarthBuild

<!--
  TODO: It would be good to add more details here about the project's governance,
  roadmap, and where to find community support (e.g., Slack, Discord, GitHub Discussions).
  This is critical information for any organization considering this migration.
-->

EarthBuild is a community-driven project. This means development is no longer backed by a single corporation but by a collective of users and contributors.

- **Stability**: The immediate goal of EarthBuild is to provide a secure, stable & reliable build tool for the community.
- **Open Governance**: The project aims for an open and transparent governance model.
- **Community Support**: Support is available through community channels.

## Key Changes

The most significant change is the removal of all features related to Earthly's commercial cloud offering.
EarthBuild focuses on being a great, self-hosted build tool.

Features related to the cloud-hosted earthly commercial offering were removed in the [final release of earthly
`v0.8.16`](https://github.com/earthly/earthly/releases/tag/v0.8.16) and will never be present in EarthBuild
releases.

We will maintain compatibility while logging warnings for other, more invasive, changes for releases of
EarthBuild on the `v0.8.x` minor version.

We will publish a breaking change to these features in the first unique minor version for EarthBuild, `v0.9.x`.

These changes include renaming of configuration variables from `EARTHLY_*` to `EARTH_*` and naming of
built-in arguments like `ARG EARTHLY_GIT_PROJECT_NAME` to `ARG EARTH_GIT_PROJECT_NAME`. Some
cloud-related syntax, such as the `PROJECT` command, is deprecated but not yet scheduled for removal —
see the Syntax section below.

### Binary Name Change

The command-line tool has been renamed from `earthly` to `earth`. You will need to update your scripts, CI configurations, and any local aliases.

```diff
- earthly +all
+ earth +all
```

In the [`earthbuild/actions-setup`](https://github.com/EarthBuild/actions-setup) GitHub Action we install a
deprecated `earthly` alias alongside `earth`, logging the deprecated usage, to ease the switch. In version
`v0.9.0` we will release a breaking change that removes the alias.

**This alias exists only in that action.** The [installation scripts](https://www.earthbuild.dev/install.html),
the published container images, and third-party packages install `earth` only. So if any of the following
describe you, there is no period of overlap and `earthly` stops working the moment you upgrade:

- your CI bakes the binary into a self-hosted runner image;
- you invoke `earthly` from a shell script, `Makefile`, `Tiltfile` or git hook;
- you install via a package manager rather than `actions-setup`.

In those cases, rename the call sites before you upgrade — or add your own shim (`ln -s "$(command -v earth)"
/usr/local/bin/earthly`) to buy yourself the same overlap deliberately.

Where the alias *does* apply, we recommend using that period of overlap to update your CI configuration in
preparation for the release.

### Installation

To switch to EarthBuild, you will need to use [the new installation scripts](https://www.earthbuild.dev/install.html).

You should remove the old `earthly` binary from your systems to avoid confusion.

### Release Signing Key Change

EarthBuild signs its releases with a **new PGP key**. If you verify release checksums, you must import it:

|             | Earthly (old)                             | EarthBuild (new)                          |
| ----------- | ----------------------------------------- | ----------------------------------------- |
| Fingerprint | `5816 B221 3DD1 CEB6 1FC9 52BA B118 5ECA 33F8 EB64` | `0890 0479 B981 AF7C 32C8 B918 604C 8879 FF83 C260` |
| Identity    | `earthly <...>`                           | `earthbuild <webmaster@earthbuild.dev>`   |
| Key file    | `https://pkg.earthly.dev/earthly.pgp`     | [`earthbuild-pgp-public.pgp`](https://raw.githubusercontent.com/EarthBuild/earthbuild/main/release/apt-repo/earthbuild-pgp-public.pgp) |

The old key will not verify EarthBuild releases, and `pkg.earthly.dev` no longer resolves — any
automation that fetched the key from that host needs updating. See
[Checksum Verification](alt-installation/alt-installation.md#checksum-verification) for the full
procedure.

Relatedly, the deb and rpm repositories that were hosted on `pkg.earthly.dev` are gone; EarthBuild
has not yet published replacements. Install the binary directly or via Homebrew in the meantime.

### Release Artifact Name Change

Release assets are now named `earth-<os>-<arch>` rather than `earthly-<os>-<arch>` (for example
`earth-linux-amd64`). Scripts that download a pinned asset URL — including
`releases/latest/download/earthly-...` links — need updating.

### Earth Directory Name Change

The Earthly directory (for config, etc.) has been renamed from `~/.earthly` to `~/.earth` in `v0.8.19`.

**`earth` does not read, copy, or warn about the old location.** Upgrading with a config only at
`~/.earthly/config.yml` silently falls back to built-in defaults — no error, no warning — so a configured
`buildkit_host`, registry mirror or `secret_provider` quietly stops applying and the build changes behaviour
for no visible reason. Move it before you upgrade:

```bash
mkdir -p ~/.earth && cp -a ~/.earthly/. ~/.earth/ && mv ~/.earthly ~/.earthly.bak
```

The same applies to anything that computes the path itself — CI steps, dotfiles, or wrapper scripts along the
lines of `${EARTHLY_CONFIG:-$HOME/.earthly/config.yml}`.

(Tracked in [#960](https://github.com/EarthBuild/earthbuild/issues/960) — the silent fallback is arguably a
bug, and this note should get simpler if we add a warning or a compatibility read.)

## Removed Features and Alternatives

The following commands and flags, mostly related to Earthly Cloud, have been removed.

### Removed Commands

| Command(s)                | Description                                    | Alternative / Migration Path                                                                                                                                                                                             |
| ------------------------- | ---------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `account`                 | Managed Earthly accounts.                      | Not applicable. EarthBuild does not have a concept of user accounts.                                                                                                                                                     |
| `org`, `orgs`             | Managed Earthly organizations.                 | Not applicable.                                                                                                                                                                                                          |
| `project`, `projects`     | Managed Earthly projects.                      | Not applicable.                                                                                                                                                                                                          |
| `satellite`, `satellites` | Managed remote runners (Buildkitd instances).  | You can run your own Buildkitd instances on any infrastructure and connect to them using `earth --buildkit-host <host>`. See [remote buildkit documentation](ci-integration/remote-buildkit.md).                    |
| `cloud`, `clouds`         | Configured Cloud Installations for BYOC plans. | See `satellite` alternative.                                                                                                                                                                                             |
| `secret`, `secrets`       | Managed cloud secrets.                         | Use standard environment variables, `--secret` flags with local files (`--secret-file-path`), or integrate with your own secret management solution (e.g., HashiCorp Vault, AWS Secrets Manager) within your Earthfiles. |
| `registry`, `registries`  | Managed registry access.                       | Removed as part of the cloud teardown in earthly `v0.8.16`. Use standard Docker authentication methods for registry access.                                                                                               |
| `web`                     | Opened the Earthly Cloud web UI.               | Not applicable.                                                                                                                                                                                                          |
| `billing`                 | Viewed Earthly billing information.            | Not applicable.                                                                                                                                                                                                          |
| `gha`                     | Managed GitHub Actions integrations.           | The core GitHub Actions integration remains. See the CI section below. This command was for a specific, now-removed, part of that integration.                                                                           |
| `prune-auto-skip`         | Pruned auto-skip data.                         | This maintenance command has been removed. The `auto-skip` feature itself is deprecated (see below); we are collecting feedback on whether to remove it in the future.                                                     |

### Removed & Changed CLI Options

- `--satellite`, `--sat`, `--no-satellite`, `--no-sat`: Removed. Use `--buildkit-host` (or configuration) explicitly to connect to a remote Buildkitd instance.
- `--auto-skip`, `--no-auto-skip` (and `--auto-skip-db-path`): **Deprecated.** These flags log a
  deprecation warning. Note that the cloud backend that once powered auto-skip has been removed; only
  the local database (`--auto-skip-db-path`) still functions. We may remove these in a future release
  and are collecting feedback to help decide — let us know how you use auto-skip in
  [this discussion](https://github.com/orgs/EarthBuild/discussions/707).
- `--auth-token`: This flag has been removed since it was used for authenticating with Earthly Cloud. For registry authentication, use standard Docker authentication methods.
- The binary name in help texts and other places is now `earth` instead of `earthly`.

### Removed Config File Options

- `disable_log_sharing`: Removed. There is no cloud provider to share logs with anymore.
- `disable_analytics`: Removed. There are no analytics to collect/share anymore.

These options are simply ignored if present — with no error and no warning, so grep your config files rather
than waiting for the tool to tell you.

### Environment Variable Changes

All `EARTHLY_*` environment variables have been renamed to `EARTH_*` to reflect the project's new identity. The following environment variables are affected:

#### Removed Environment Variables

The following environment variables have been removed along with their associated features:

- `EARTHLY_TOKEN` - Used for Earthly Cloud authentication
- `EARTHLY_SATELLITE` - Selected satellite for builds
- `EARTHLY_NO_SATELLITE` - Disabled satellite usage

The `auto-skip` environment variables (`EARTHLY_AUTO_SKIP`, `EARTHLY_NO_AUTO_SKIP`,
`EARTHLY_AUTO_SKIP_DB_PATH`) are **not** removed. Like the corresponding flags, they are deprecated and
log a warning; we are collecting feedback on whether to remove them in the future (see the
[auto-skip discussion](https://github.com/orgs/EarthBuild/discussions/707)).

#### Migration Strategy

**Immediate:** EarthBuild will continue to recognize `EARTHLY_*` environment variables in the current version but will log deprecation warnings encouraging migration to `EARTH_*` variables.

**Future Breaking Change:** In version `v0.9.0` and onwards, support for `EARTHLY_*` environment variables will be removed entirely. You must update your environment configurations before upgrading to that version.

**Precedence:** when both spellings are set, `EARTH_*` wins regardless of which was set first, and the
deprecation warning still fires for the `EARTHLY_*` one. You can therefore set both during a staged rollout
without changing behaviour.

**Look beyond your own repository.** The warning fires whenever an `EARTHLY_*` variable is merely *present*
in the environment, even when it is being ignored:

```
$ EARTHLY_CONFIG=./a.yml EARTH_CONFIG=./b.yml earth ls
loading config values from "./b.yml"
WARNING: EARTHLY_CONFIG is deprecated. Use EARTH_CONFIG.
```

So these warnings survive a complete and correct rename of everything you control, which makes "am I done?"
hard to answer from the tool's output alone. The residue is usually somewhere outside the repo:

- `ENV EARTHLY_…` baked into a self-hosted runner image;
- Kubernetes pod specs, runner scale-set templates or Helm values;
- CI platform-level, organisation-level or repository-level variables;
- developer shell profiles.

The warning names the variable but not its source, so `env | grep EARTHLY_` inside a failing job is usually
the fastest way to find it.

**Standard Variables Unchanged:** Some environment variables remain unchanged as they follow standard conventions:

- `GIT_USERNAME` - Git authentication username
- `GIT_PASSWORD` - Git authentication password
- `GITHUB_ACTIONS` - GitHub Actions environment detection
- `BUILDKIT_HOST` - Read by the all-in-one image to point at an external BuildKit daemon.
  This is upstream BuildKit's own variable name, not an Earthly one, so it keeps its
  unprefixed spelling. Note that the `earth` CLI flag binding is separate and *is* renamed,
  to `EARTH_BUILDKIT_HOST`.

#### Container and Image Variables

The variables consumed by the published images — `EARTH_ADDITIONAL_BUILDKIT_CONFIG`,
`EARTH_EXEC_CMD`, `EARTH_TMP_DIR`, `EARTH_RESET_TMP_DIR`, `EARTH_DEBUG` and
`EARTH_BUILDKIT_HOST` — follow the same rule: the `EARTH_` name is current, and the
`EARTHLY_` spelling still works but logs a deprecation warning.

The variables that the `earth` CLI previously used to configure `WITH DOCKER`
(`EARTHLY_DOCKERD_DATA_ROOT`, `EARTHLY_START_COMPOSE`, `EARTHLY_COMPOSE_FILES` and
friends) are gone entirely rather than renamed. They were an internal protocol between
the CLI and the buildkitd image and are now passed as command-line flags, so there is
nothing to set. `EARTHLY_DOCKER_WRAPPER_DEBUG`, `EARTHLY_DOCKER_WRAPPER_DEBUG_CMD` and
`EARTHLY_DOCKER_WRAPPER_PRE_SCRIPT` remain environment variables, renamed to `EARTH_*`
with the usual deprecation fallback.

### Buildkitd Container Name Changes

The buildkitd container name has changed from `earthly-buildkitd` to `earth-buildkitd`.

### Buildkitd Cache Volume Changes

The buildkitd cache volume name has changed from `earthly-cache` to `earth-cache`.

### Which release these renames land in

The installation name is a compile-time value that drives all three of the names above, plus the config
directory. **It changed in `v0.8.19`.** Releases up to and including `v0.8.18` still use the `earthly`
spellings even though they carry the EarthBuild name, so a guide step that looks wrong on your system may
simply be describing a release you are not on yet:

| | up to `v0.8.18` | `v0.8.19` and later |
| --- | --- | --- |
| buildkitd container | `earthly-buildkitd` | `earth-buildkitd` |
| cache volume | `earthly-cache` | `earth-cache` |
| config directory | `~/.earthly` | `~/.earth` |

This matters most when your CLI and your BuildKit daemons upgrade separately — a common shape in CI, where
the daemon is a long-lived container or DaemonSet. Check both sides.

Note that a `--buildkit-host docker-container://<name>` value *embeds* the container name. The `EARTHLY_*`
compatibility shim covers variable **names**, not values, so a stale name here is a hard failure rather than
a deprecation warning:

```
build new buildkitd client: maybe start buildkitd: wait until started:
  expected address to be docker-container://earth-buildkitd,
  but got docker-container://earthly-buildkitd
```

---

## Detailed CLI Diff

Here is a `diff` of the CLI help output to highlight the changes. The `+` side below is captured from
EarthBuild `v0.8.19`.

> Note: `v0.8.19` lists both spellings for each binding — for example
> `--config string  Path to config file [$EARTH_CONFIG, $EARTHLY_CONFIG]`. `EARTH_*` is the one to
> use — see the Environment Variable Changes section above.

```diff
NAME:
-   earthly - The CI/CD framework that runs anywhere!
+   earth - The CI/CD framework that runs anywhere!

USAGE:
-        earthly [options] <target-ref>
+        earth [options] <target-ref>
-        earthly [options] --image <target-ref>
+        earth [options] --image <target-ref>
-        earthly [options] --artifact <target-ref>/<artifact-path> [<dest-path>]
+        earth [options] --artifact <target-ref>/<artifact-path> [<dest-path>]
-        earthly [options] command [command options]
+        earth [options] command [command options]


COMMANDS:
   bootstrap                   Bootstraps earth installation including buildkit image download and optionally shell autocompletion
   docker-build                *beta* Build a Dockerfile without an Earthfile
-   account                     Create or manage an Earthly account
   config                      Edits your earth configuration file
   doc                         Document targets from an Earthfile
   init                        *experimental* Initialize an Earthfile for the current project
   ls                          List targets from an Earthfile
-   org, orgs                   Create or manage your Earthly orgs
-   project, projects           Manage Earthly projects
   prune                       Prune earth build cache
-   prune-auto-skip             Prune Earthly auto-skip data
-   registry, registries        *beta* Manage registry access
-   satellite, satellites, sat  Create and manage Earthly Satellites
-   cloud, clouds               Configure Cloud Installations for BYOC plans
-   secret, secrets             *beta* Manage cloud secrets
-   web                         *beta* Access the web UI via your default browser and print the url
-   billing, bill               *experimental* View Earthly billing info
-   gha                         *experimental* Manage GitHub Actions integrations
   help, h                     Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --config string                  Path to config file [$EARTHLY_CONFIG]
   --ssh-auth-sock string           The SSH auth socket to use for ssh-agent forwarding [$EARTHLY_SSH_AUTH_SOCK]
   --git-username string            The git username to use for git HTTPS authentication [$GIT_USERNAME]
   --git-password string            The git password to use for git HTTPS authentication [$GIT_PASSWORD]
   --verbose, -V                    Enable verbose logging [$EARTHLY_VERBOSE]
   --buildkit-host string           The URL to use for connecting to a buildkit host
                                      If empty, earth will attempt to start a buildkitd instance via docker run [$EARTHLY_BUILDKIT_HOST]
   --env-file-path string           Use values from this file as earth environment variables; values are no longer used as --build-arg's or --secret's (default: ".env") [$EARTHLY_ENV_FILE_PATH]
   --arg-file-path string           Use values from this file as earth buildargs (default: ".arg") [$EARTHLY_ARG_FILE_PATH]
   --secret-file-path string        Use values from this file as earth secrets (default: ".secret") [$EARTHLY_SECRET_FILE_PATH]
   --artifact, -a                   Output specified artifact; a wildcard (*) can be used to output all artifacts
   --image                          Output only docker image of the specified target
   --push                           Push docker images and execute RUN --push commands [$EARTHLY_PUSH]
   --ci                             Execute in CI mode.
                                    Implies --no-output --strict [$EARTHLY_CI]
   --output                         Allow artifacts or images to be output, even when running under --ci mode [$EARTHLY_OUTPUT]
   --no-output                      Do not output artifacts or images
                                    (using --push is still allowed) [$EARTHLY_NO_OUTPUT]
   --no-cache                       Do not use cache while building [$EARTHLY_NO_CACHE]
   --auto-skip                      Skip buildkit if target has already been built [$EARTHLY_AUTO_SKIP]
   --allow-privileged, -P           Allow build to use the --privileged flag in RUN commands [$EARTHLY_ALLOW_PRIVILEGED]
   --max-remote-cache               Saves all intermediate images too in the remote cache [$EARTHLY_MAX_REMOTE_CACHE]
   --save-inline-cache              Enable cache inlining when pushing images [$EARTHLY_SAVE_INLINE_CACHE]
   --use-inline-cache               Attempt to use any inline cache that may have been previously pushed
                                    uses image tags referenced by SAVE IMAGE --push or SAVE IMAGE --cache-from [$EARTHLY_USE_INLINE_CACHE]
   --interactive, -i                Enable interactive debugging [$EARTHLY_INTERACTIVE]
   --strict                         Disallow usage of features that may create unrepeatable builds [$EARTHLY_STRICT]
   --auto-skip-db-path string       use a local database for auto-skip [$EARTHLY_AUTO_SKIP_DB_PATH]
   --buildkit-image string          The docker image to use for the buildkit daemon (default: "docker.io/earthbuild/buildkitd:v0.8.19") [$EARTHLY_BUILDKIT_IMAGE]
   --remote-cache string            A remote docker image tag use as explicit cache and optionally additional attributes to set in the image (Format: "<image-tag>[,<attr1>=<val1>,<attr2>=<val2>,...]") [$EARTHLY_REMOTE_CACHE]
   --disable-remote-registry-proxy  Don't use the Docker registry proxy when transferring images [$EARTHLY_DISABLE_REMOTE_REGISTRY_PROXY]
   --no-auto-skip                   Disable auto-skip functionality [$EARTHLY_NO_AUTO_SKIP]
   --github-annotations             Enable GitHub Actions workflow specific output [$GITHUB_ACTIONS]
   --help, -h                       show help
   --version, -v                    print the version
```

## Syntax

The core syntax of Earthfiles is largely unchanged.

The `PROJECT` command relates to the cloud offering. It is still accepted, but logs a deprecation
warning. With the cloud integration removed, it no longer has any effect unless you use a custom
secret command. We may remove it in a future release and are collecting feedback to help decide — let
us know how you use `PROJECT` in [this discussion](https://github.com/orgs/EarthBuild/discussions/708).

Built-in arguments are renamed from `ARG EARTHLY_*` to `ARG EARTH_*`. The `EARTHLY_*` names still
work in `v0.8.x`, but referencing one logs a deprecation warning pointing at the `EARTH_*` equivalent
(for example, `ARG EARTHLY_GIT_PROJECT_NAME` warns and suggests `EARTH_GIT_PROJECT_NAME`). The
`EARTHLY_*` built-in arguments will be removed in `v0.9.x`. See the [built-in args
reference](earthfile/builtin-args.md) for the full list.

The obsolete `EARTHLY_CI_RUNNER` env variable and `VERSION --earthly-ci-runner-arg` feature flag have been removed.

## CI

### GitHub Actions

If you use the github actions CI integration (formerly
[`earthly/actions-setup`](https://github.com/earthly/actions-setup)), you should update your workflow yaml to
point to [`EarthBuild/actions-setup`](https://github.com/EarthBuild/actions-setup) instead.

```diff
   build:
     runs-on: ubuntu-latest
     steps:
       - uses: actions/checkout@v4
       - name: Setup Earthly
-         uses: earthly/actions-setup@main
+         uses: earthbuild/actions-setup@main
         with:
-          earthly-version: v0.8.5 # example
+          version: v0.8.17 # example, use the latest earthbuild version
       - name: Run build
-         run: earthly --ci +all
+         run: earth --ci +all
```

## Container Image Registries

The canonical container images have moved from the `docker.io/earthly/*` namespace to
`docker.io/earthbuild/*`. If you reference any of these images directly — for example via
`--buildkit-image`, the `global.buildkit_image` config value, a `WITH DOCKER --pull`, or a CI step
that pulls the all-in-one image — update the registry path.

| Image                          | Old (earthly)                  | New (EarthBuild)                  |
| ------------------------------ | ------------------------------ | -------------------------------- |
| All-in-one CLI image           | `docker.io/earthly/earthly`    | `docker.io/earthbuild/earthbuild` |
| BuildKit daemon                | `docker.io/earthly/buildkitd`  | `docker.io/earthbuild/buildkitd`  |
| Docker-in-Docker (`WITH DOCKER`) | `docker.io/earthly/dind`     | `docker.io/earthbuild/dind`       |

Concrete examples as they apply to `v0.8.18`:

```diff
# Pinning the buildkit daemon image
- earthly --buildkit-image docker.io/earthly/buildkitd:v0.8.15 +all
+ earth --buildkit-image docker.io/earthbuild/buildkitd:v0.8.18 +all

# Pulling the all-in-one image in CI
- docker pull docker.io/earthly/earthly:v0.8.16
+ docker pull docker.io/earthbuild/earthbuild:v0.8.19

# A dind base image (WITH DOCKER)
- FROM docker.io/earthly/dind:alpine
+ FROM docker.io/earthbuild/dind:alpine-3.24-docker-29.5.3-r1
```

#### `earthbuild/dind` has no floating tags

`earthbuild/dind` publishes **only fully-pinned tags** — `alpine-3.24-docker-29.5.3-r1`,
`ubuntu-26.04-docker-29.8.1-1`, and so on. The rolling `latest`, `alpine` and `ubuntu` tags that
`earthly/dind` carried do **not** exist, so a bare `FROM earthly/dind` or `FROM earthly/dind:alpine` has no
drop-in replacement and the substitution in the table above is not purely mechanical for this image. Pick a
tag from [the tag list](https://hub.docker.com/r/earthbuild/dind/tags) and add it to whatever keeps your pins
current.

(Tracked in [#961](https://github.com/EarthBuild/earthbuild/issues/961).)

For most users the BuildKit daemon image does not need to be set explicitly — `earth` defaults to the
matching `docker.io/earthbuild/buildkitd` image for the release automatically. You only need to act if
you have pinned an `earthly/*` image path somewhere.

## Other repositories

For other repositories in the earthly ecosystem, we've created EarthBuild forks:

- [earthly/lib](https://github.com/earthly/lib) -> [EarthBuild/lib](https://github.com/EarthBuild/lib),
  currently tagged `3.0.4`. `IMPORT github.com/earthly/lib:3.0.3` becomes
  `IMPORT github.com/EarthBuild/lib:3.0.4` — a drop-in replacement, with `+INSTALL_DIND` and friends
  unchanged.
- [earthly/dind](https://github.com/earthly/dind) -> [earthbuild/dind](https://github.com/EarthBuild/dind)
- [earthly/actions-setup](https://github.com/earthly/actions-setup) -> [earthbuild/actions-setup](https://github.com/EarthBuild/actions-setup)

## Hint 🤖

Provide this document to your agent of choice to pick up the heavy lifting at your org.