# Migrating from `earthly` to EarthBuild

[`earthly`](https://github.com/earthly/earthly) was originally developed by [Earthly
Technologies](https://earthly.dev) as a corporately-sponsored open-source project.

In 2025, earthly [announced a pivot to a different business
model](https://web.archive.org/web/20250420142821/https://earthly.dev/blog/shutting-down-earthfiles-cloud/),
no longer maintaining `earthly` to focus on entirely different products and directions.

## Features removed

In earthly's original monitization model, they offered managed hosting of `earthly/buildkitd` instances
through their "satellite" product.

This included clouds, accounts, billing, cloud secret management and "value add" features like "auto skip."

In the community fork of earthly, no monetization is attempted; therefore these features no longer make sense
and have been removed from the earthly CLI binary.

### CLI Commands

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
   bootstrap                   Bootstraps earthly installation including buildkit image download and optionally shell autocompletion
   docker-build                *beta* Build a Dockerfile without an Earthfile
-   account                     Create or manage an Earthly account
   config                      Edits your Earthly configuration file
   doc                         Document targets from an Earthfile
   init                        *experimental* Initialize an Earthfile for the current project
   ls                          List targets from an Earthfile
-   org, orgs                   Create or manage your Earthly orgs
-   project, projects           Manage Earthly projects
   prune                       Prune Earthly build cache
-   prune-auto-skip             Prune Earthly auto-skip data
   registry, registries        *beta* Manage registry access
-   satellite, satellites, sat  Create and manage Earthly Satellites
-   cloud, clouds               Configure Cloud Installations for BYOC plans
-   secret, secrets             *beta* Manage cloud secrets
-   web                         *beta* Access the web UI via your default browser and print the url
-   billing, bill               *experimental* View Earthly billing info
-   gha                         *experimental* Manage GitHub Actions integrations
   help, h                     Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --config value                   Path to config file [$EARTHLY_CONFIG]
   --ssh-auth-sock value            The SSH auth socket to use for ssh-agent forwarding (default: "/private/tmp/com.apple.launchd.ZviWbhl8ar/Listeners") [$EARTHLY_SSH_AUTH_SOCK]
   --auth-token value               Force Earthly account login to authenticate with supplied token [$EARTHLY_TOKEN]
   --git-username value             The git username to use for git HTTPS authentication [$GIT_USERNAME]
   --git-password value             The git password to use for git HTTPS authentication [$GIT_PASSWORD]
   --verbose, -V                    Enable verbose logging (default: false) [$EARTHLY_VERBOSE]
   --buildkit-host value            The URL to use for connecting to a buildkit host
                                      If empty, earthly will attempt to start a buildkitd instance via docker run [$EARTHLY_BUILDKIT_HOST]
                                    Disable collection of analytics (default: false) [$EARTHLY_DISABLE_ANALYTICS, $DO_NOT_TRACK]
   --env-file-path value            Use values from this file as earthly environment variables; values are no longer used as --build-arg's or --secret's (default: ".env") [$EARTHLY_ENV_FILE_PATH]
   --arg-file-path value            Use values from this file as earthly buildargs (default: ".arg") [$EARTHLY_ARG_FILE_PATH]
   --secret-file-path value         Use values from this file as earthly secrets (default: ".secret") [$EARTHLY_SECRET_FILE_PATH]
   --artifact, -a                   Output specified artifact; a wildcard (*) can be used to output all artifacts (default: false)
   --image                          Output only docker image of the specified target (default: false)
   --push                           Push docker images and execute RUN --push commands (default: false) [$EARTHLY_PUSH]
   --ci                             Execute in CI mode.
                                    Implies --no-output --strict (default: false) [$EARTHLY_CI]
   --output                         Allow artifacts or images to be output, even when running under --ci mode (default: false) [$EARTHLY_OUTPUT]
   --no-output                      Do not output artifacts or images
                                    (using --push is still allowed) (default: false) [$EARTHLY_NO_OUTPUT]
   --no-cache                       Do not use cache while building (default: false) [$EARTHLY_NO_CACHE]
   --auto-skip                      Skip buildkit if target has already been built (default: false) [$EARTHLY_AUTO_SKIP]
   --allow-privileged, -P           Allow build to use the --privileged flag in RUN commands (default: false) [$EARTHLY_ALLOW_PRIVILEGED]
   --max-remote-cache               Saves all intermediate images too in the remote cache (default: false) [$EARTHLY_MAX_REMOTE_CACHE]
   --save-inline-cache              Enable cache inlining when pushing images (default: false) [$EARTHLY_SAVE_INLINE_CACHE]
   --use-inline-cache               Attempt to use any inline cache that may have been previously pushed
                                    uses image tags referenced by SAVE IMAGE --push or SAVE IMAGE --cache-from (default: false) [$EARTHLY_USE_INLINE_CACHE]
   --interactive, -i                Enable interactive debugging (default: false) [$EARTHLY_INTERACTIVE]
   --strict                         Disallow usage of features that may create unrepeatable builds (default: false) [$EARTHLY_STRICT]
-   --satellite value, --sat value   The name of satellite to use for this build. [$EARTHLY_SATELLITE]
   --no-satellite, --no-sat         Disables the use of a selected satellite for this build. (default: false) [$EARTHLY_NO_SATELLITE]
   --buildkit-image value           The docker image to use for the buildkit daemon (default: "docker.io/earthly/buildkitd:v0.8.15") [$EARTHLY_BUILDKIT_IMAGE]
   --remote-cache value             A remote docker image tag use as explicit cache and optionally additional attributes to set in the image (Format: "<image-tag>[,<attr1>=<val1>,<attr2>=<val2>,...]") [$EARTHLY_REMOTE_CACHE]
   --disable-remote-registry-proxy  Don't use the Docker registry proxy when transferring images (default: false) [$EARTHLY_DISABLE_REMOTE_REGISTRY_PROXY]
-   --no-auto-skip                   Disable auto-skip functionality (default: false) [$EARTHLY_NO_AUTO_SKIP]
   --github-annotations             Enable Git Hub Actions workflow specific output (default: false) [$GITHUB_ACTIONS]
   --help, -h                       show help
   --version, -v                    print the version
```

TODO:

- `--no-satellite` Is there an equivalent?
- what is `--github-annotations`?

Commands removed

- Billing
- 

### CLI Options

- Autoskip TODO: add a link to a ticket to bring this back

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
       - name: Run build
         run: earthly --ci +all
```
