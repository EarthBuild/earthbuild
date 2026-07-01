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

These changes include renaming of configuration variables from `EARTHLY_*` to `EARTH_*`, removal of Earthfile syntax related to cloud
hosting like `PROJECT` and naming of built-in arguments like `ARG EARTHLY_GIT_PROJECT_NAME` to `ARG EARTH_GIT_PROJECT_NAME`.

### Binary Name Change

The command-line tool has been renamed from `earthly` to `earth`. You will need to update your scripts, CI configurations, and any local aliases.

```diff
- earthly +all
+ earth +all
```

In the `earthlybuild/actions-setup` github action, we've aliased `earthly` to `earth`, logging the deprecated
usage, to ease the switch.

In version `v0.9.0` we will release a breaking change that removes the alias.

As of that version, you must update your CI configuration to use `earth` instead of `earthly` to reference the
CLI binary.

We recommend using this period of overlap to update your CI configuration in preparation of the release.

### Installation

To switch to EarthBuild, you will need to use [the new installation scripts](https://www.earthbuild.dev/install.html).

You should remove the old `earthly` binary from your systems to avoid confusion.

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
| `prune-auto-skip`         | Pruned auto-skip data.                         | This maintenance command has been removed. The `auto-skip` feature itself is deprecated (see below) and will be removed in `v0.9.x`.                                                                                       |

### Removed & Changed CLI Options

- `--satellite`, `--sat`, `--no-satellite`, `--no-sat`: Removed. Use `--buildkit-host` (or configuration) explicitly to connect to a remote Buildkitd instance.
- `--auto-skip`, `--no-auto-skip` (and `--auto-skip-db-path`): **Deprecated, not yet removed.** These
  flags still function in `v0.8.x` but will log a deprecation warning and be removed in `v0.9.x`.
- `--auth-token`: This flag has been removed since it was used for authenticating with Earthly Cloud. For registry authentication, use standard Docker authentication methods.
- The binary name in help texts and other places is now `earth` instead of `earthly`.

### Environment Variable Changes

All `EARTHLY_*` environment variables have been renamed to `EARTH_*` to reflect the project's new identity. The following environment variables are affected:

#### Removed Environment Variables

The following environment variables have been removed along with their associated features:

- `EARTHLY_TOKEN` - Used for Earthly Cloud authentication
- `EARTHLY_SATELLITE` - Selected satellite for builds
- `EARTHLY_NO_SATELLITE` - Disabled satellite usage

The `auto-skip` environment variables (`EARTHLY_AUTO_SKIP`, `EARTHLY_NO_AUTO_SKIP`,
`EARTHLY_AUTO_SKIP_DB_PATH`) are **not** removed. Like the corresponding flags, they are deprecated in
`v0.8.x` and will be removed in `v0.9.x`.

#### Migration Strategy

**Immediate:** EarthBuild will continue to recognize `EARTHLY_*` environment variables in the current version but will log deprecation warnings encouraging migration to `EARTH_*` variables.

**Future Breaking Change:** In version `v0.9.0` and onwards, support for `EARTHLY_*` environment variables will be removed entirely. You must update your environment configurations before upgrading to that version.

**Standard Variables Unchanged:** Some environment variables remain unchanged as they follow standard conventions:

- `GIT_USERNAME` - Git authentication username
- `GIT_PASSWORD` - Git authentication password
- `GITHUB_ACTIONS` - GitHub Actions environment detection

---

## Detailed CLI Diff

Here is a `diff` of the CLI help output to highlight the changes. The `+` side below is captured from
EarthBuild `v0.8.18`.

> Note: environment-variable bindings are still shown with the `EARTHLY_*` prefix. The rename to
> `EARTH_*` (with deprecation warnings) is tracked separately and lands ahead of the `v0.9.x` breaking
> change — see the Environment Variable Changes section above.

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
   --buildkit-image string          The docker image to use for the buildkit daemon (default: "docker.io/earthbuild/buildkitd:v0.8.18") [$EARTHLY_BUILDKIT_IMAGE]
   --remote-cache string            A remote docker image tag use as explicit cache and optionally additional attributes to set in the image (Format: "<image-tag>[,<attr1>=<val1>,<attr2>=<val2>,...]") [$EARTHLY_REMOTE_CACHE]
   --disable-remote-registry-proxy  Don't use the Docker registry proxy when transferring images [$EARTHLY_DISABLE_REMOTE_REGISTRY_PROXY]
   --no-auto-skip                   Disable auto-skip functionality [$EARTHLY_NO_AUTO_SKIP]
   --github-annotations             Enable GitHub Actions workflow specific output [$GITHUB_ACTIONS]
   --help, -h                       show help
   --version, -v                    print the version
```

## Syntax

The core syntax of Earthfiles is largely unchanged.

Again, this will be logged as a warning in `v0.8.x` and removed, treated as an error, in `v0.9.x`.

The `PROJECT` command relates to the cloud offering. It is still accepted in `v0.8.x` where it will
log a deprecation warning, and it will be removed and treated as an error in `v0.9.x`.

Built-in arguments are renamed from `ARG EARTHLY_*` to `ARG EARTH_*`.

## CI

### GitHub Actions

If you use the github actions CI integration (formerly
[`github.com/earthly/actions-setup`](github.com/earthly/actions-setup)), you should update your workflow yaml to
point to [`github.com/earthbuild/actions-setup`](github.com/earthbuild/actions-setup) instead.

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
+ docker pull docker.io/earthbuild/earthbuild:v0.8.18

# A dind base image (WITH DOCKER)
- FROM docker.io/earthly/dind:alpine
+ FROM docker.io/earthbuild/dind:alpine-3.23-docker-29.5.2-r0
```

For most users the BuildKit daemon image does not need to be set explicitly — `earth` defaults to the
matching `docker.io/earthbuild/buildkitd` image for the release automatically. You only need to act if
you have pinned an `earthly/*` image path somewhere.

## Other repositories

For other repositories in the earthly ecosystem, we've created EarthBuild forks:

<!-- TODO: push tags, establish our first tag and document -->
- [earthly/lib](https://github.com/earthly/lib) -> [earthbuild/lib](https://github.com/EarthBuild/lib)
- [earthly/dind](https://github.com/earthly/dind) -> [earthbuild/dind](https://github.com/EarthBuild/dind)
- [earthly/actions-setup](https://github.com/earthly/actions-setup) -> [earthbuild/actions-setup](https://github.com/EarthBuild/actions-setup)

## Hint 🤖

Provide this document to your agent of choice to pick up the heavy lifting at your org.