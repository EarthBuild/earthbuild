# EarthBuild command reference

{% hint style='danger' %}

##### Removed commands

The commands below were documented here previously and **no longer exist**. They belonged to
EarthBuild's commercial cloud offering, which was shut down; EarthBuild is self-hosted only.

| Removed                   | Instead                                                                                            |
| ------------------------- | -------------------------------------------------------------------------------------------------- |
| `satellite`, `sat`        | Run your own BuildKit instance and connect with `--buildkit-host`. See [remote runners](../remote-runners.md). |
| `registry`, `registries`  | Use standard Docker authentication (`docker login`, credential helpers).                             |
| `secret`, `secrets`       | Use `--secret`, `--secret-file-path`, or your own secret manager. See the [secrets guide](../guides/secrets.md). |
| `project`, `projects`     | Not applicable — EarthBuild has no concept of projects.                                              |
| `org`, `orgs`, `account`  | Not applicable — EarthBuild has no concept of accounts or organizations.                             |
| `web`, `billing`, `gha`   | Not applicable.                                                                                      |
| `prune-auto-skip`         | Delete the local auto-skip database file directly.                                                   |

See [Migrating from earth](../migrating-from-earthly.md) for the full migration guide.

{% endhint %}

## earth

#### Synopsis

- Target form
  ```
  earth [options...] <target-ref> [build-args...]
  ```
- Artifact form
  ```
  earth [options...] --artifact|-a <target-ref>/<artifact-path> [<dest-path>] [build-args...]
  ```
- Image form
  ```
  earth [options...] --image <target-ref> [build-args...]
  ```

#### Description

The earth command executes a build referenced by `<target-ref>` (*target form* and *image form*) or `<artifact-ref>` (*artifact form*). In the *target form*, the referenced target and its dependencies are built. In the *artifact form*, the referenced artifact and its dependencies are built, but only the specified artifact is output. The output path of the artifact can be optionally overridden by `<dest-path>`. In the *image form*, the image produced by the referenced target and its dependencies are built, but only the specified image is output.

If a BuildKit daemon has not already been started, and the option `--buildkit-host` is not specified, this command also starts up a container named `earthly-buildkitd` to act as a build daemon.

The execution has four phases:

- Init
- Build
- Push (optional - disabled by default)
- Local output (optional - enabled by default)

During the init phase the configuration is interpreted and the BuildKit daemon is started (if applicable). During the build phase, the referenced target and all its direct or indirect dependencies are executed. During the push phase, when enabled, EarthBuild performs image pushes and it also runs `RUN --push` commands.  During the local output phase, all applicable artifacts with an `AS LOCAL` specification are written to the specified output location, and all applicable docker images are loaded onto the host's docker daemon.

If the build phase does not succeed, no output is produced and no push instruction is executed. In this case, the command exits with a non-zero exit code.

#### Target and Artifact References

The `<target-ref>` can reference both local and remote targets.

##### Local Reference

`+<target-name>` will reference a target in the local Earthfile in the current directory.

`<local-path>+<target-name>` will reference a local Earthfile in a different directory as
specified by `<local-path>`, which must start with `./`, `../`, or `/`.

##### Remote Reference

`<gitvendor>/<namespace>/<project>/path/in/project[:some-tag]+<target-name>` will access a remote git repository.

##### Artifact Reference

The `<artifact-ref>` can reference artifacts built by targets. `<target-ref>/<artifact-path>` will reference a build target's artifact.

##### Examples

See the [importing guide](../guides/importing.md) for more details and examples.

#### Build args

Synopsis:

  - Target form `earth <target-ref> [--<build-arg-key>=<build-arg-value>...]`
  - Artifact form `earth --artifact <target-ref>/<artifact-path> <dest-path> [--<build-arg-key>=<build-arg-value>...]`
  - Image form `earth --image <target-ref> [--<build-arg-key>=<build-arg-value>...]`

Also available as an env var setting: `EARTHLY_BUILD_ARGS="<build-arg-key>=<build-arg-value>,<build-arg-key>=<build-arg-value>,..."`.

Build arg overrides may be specified as part of the EarthBuild command. The value of the build arg `<build-arg-key>` is set to `<build-arg-value>`.

In the target and image forms the build args are passed after the target reference. For example `earth +some-target --NAME=john --SPECIES=human`. In the artifact form, the build args are passed immediately after the artifact reference, however they are surrounded by parenthesis, similar to a [`COPY` command](../earthfile/earthfile.md#copy). For example `earth --artifact +some-target/some-artifact ./dest/path --NAME=john --SPECIES=human`.

The build arg overrides only apply to the target being called directly and any other target referenced as part of the same Earthfile. Build arg overrides, will not apply to targets referenced from other directories or other repositories.

##### Storing values in the `.arg` File

Build args can also be specified using a `.arg` file, relative to the current working directory where `earth` is executed from, using the syntax:

```.env
<NAME_OF_BUILD_ARG>=<value>
...
```

Each variable must be specified on a separate line, without any surrounding quotes. If quotes are included, they will become part of the value.
Lines beginning with `#` are treated as comments. Blank lines are allowed. Here is a simple example:

```.env
# an example build arg
MY_SETTING=a setting which contains spaces
```

{% hint style='info' %}
##### Note
The directory used for loading the `.arg` file is the directory where `earth` is called from and not necessarily the directory where the Earthfile is located in.
{% endhint %}

{% hint style='danger' %}
##### Important
The `.arg` file is meant for settings which are specific to the local environment the build executes in. These settings may cause inconsistencies in the way the build executes on different systems, leading to builds that are difficult to reproduce. Keep the contents of `.arg` files to a minimum to avoid such issues.
{% endhint %}

##### Additional Information

For more information about build args see the [`ARG` Earthfile command](../earthfile/earthfile.md#arg), and the [build args guide](../guides/build-args.md).

#### Environment Variables and .env File

Flag options can either be set on the command line, or by using an equivalent environment variable, as specified under the [options section](#options).

It is also possible to set these flag options in an `.env` file, relative to the current working directory where `earth` is executed from, using the syntax:

```.env
<NAME_OF_ENV_VAR>=<value>
...
```

Each variable must be specified on a separate line, without any surrounding quotes. If quotes are included, they will become part of the value.
Lines beginning with `#` are treated as comments. Blank lines are allowed. Here is a simple example:

```.env
# Settings
EARTHLY_ALLOW_PRIVILEGED=true
EARTHLY_VERBOSE=true
```

### Global Options

##### `--help`

Prints help information about earth.

###### Synopsis

- ```
  earth --help
  ```
- ```
  earth <command> --help
  ```

##### `--config <path>`

Also available as an env var setting: `EARTHLY_CONFIG=<path>`.

Overrides the earth [configuration file](../earthly-config/earthly-config.md), defaults to `~/.earthly/config.yml`.

##### `--installation-name <name>`

Also available as an env var setting: `EARTHLY_INSTALLATION_NAME=<name>`.

Overrides the EarthBuild installation name. The installation name is used for the BuildKit Daemon name, the cache volume name, the configuration directory (`~/.<installation-name>`) and for the ports used by BuildKit. Using multiple installation names on the same system allows EarthBuild to run as multiple isolated instances, each with its own configuration, cache and daemon. Defaults to `earth`.

##### `--ssh-auth-sock <path-to-sock>`

Also available as an env var setting: `EARTHLY_SSH_AUTH_SOCK=<path-to-sock>`.

Sets the path to the SSH agent sock, which can be used for SSH authentication. SSH authentication is used by EarthBuild in order to perform git clone's underneath.

On Linux systems, this setting defaults to the value of the env var $SSH_AUTH_SOCK. On most systems, the env var `SSH_AUTH_SOCK` env var is already set if an SSH agent is running.

On Mac systems, this setting defaults to `/run/host-services/ssh-auth.sock` to match recommendation in [the official Docker documentation](https://docs.docker.com/docker-for-mac/osxfs/#ssh-agent-forwarding).

For more information see the [Authentication page](../guides/auth.md).

##### `--verbose`

Also available as an env var setting: `EARTHLY_VERBOSE=1`.

Enables verbose logging.

##### `--git-username <git-user>` (**deprecated**)

Also available as an env var setting: `GIT_USERNAME=<git-user>`.

This option is now deprecated. Please use the [configuration file](../earthly-config/earthly-config.md) instead.

##### `--git-password <git-pass>` (**deprecated**)

Also available as an env var setting: `GIT_PASSWORD=<git-pass>`.

This option is now deprecated. Please use the [configuration file](../earthly-config/earthly-config.md) instead.

##### `--git-url-instead-of <git-instead-of>` (**obsolete**)

Also used to be available as an env var setting: `GIT_URL_INSTEAD_OF=<git-instead-of>`.

This option is now obsolete. By default, `earth` will automatically switch from ssh to HTTPS when no keys are found or the ssh-agent isn't running.
Please use the [configuration file](../earthly-config/earthly-config.md) to override the default behavior.

### Build Options

Build options are specific to executing EarthBuild builds; they are simply listed in this section for readability, and can be supplied as global options.

##### `--secret|-s <secret-id>[=<value>]`

Also available as an env var setting: `EARTHLY_SECRETS="<secret-id>=<value>,<secret-id>=<value>,..."`.

Passes a secret with ID `<secret-id>` to the build environments. If `<value>` is not specified, then the value becomes the value of the environment variable with the same name as `<secret-id>`.

The secret can be referenced within Earthfile recipes as `RUN --secret <arbitrary-env-var-name>=<secret-id>`. For more information see the [`RUN --secret` Earthfile command](../earthfile/earthfile.md#run).

Secrets can also be stored in a `.secret` file using the same syntax as an `.arg` file; an example is given under the [secrets guide](../guides/secrets.md).

##### `--secret-file <secret-id>=<path>`

Also available as an env var setting: `EARTHLY_SECRET_FILES="<secret-id>=<path>,<secret-id>=<path>,..."`.

Loads the contents of a file located at `<path>` into a secret with ID `<secret-id>` for use within the build environments.

The secret can be referenced within Earthfile recipes as `RUN --secret <arbitrary-env-var-name>=<secret-id>`. For more information see the [`RUN --secret` Earthfile command](../earthfile/earthfile.md#run).

##### `--push`

Also available as an env var setting: `EARTHLY_PUSH=true`.

Instructs EarthBuild to push any docker images declared with the `--push` flag to remote docker registries and to run any `RUN --push` commands. For more information see the [`SAVE IMAGE` Earthfile command](../earthfile/earthfile.md#save-image) and the [`RUN --push` Earthfile command](../earthfile/earthfile.md#run).

Pushing only happens during the output phase, and only if the build has succeeded.

##### `--no-output`

Also available as an env var setting: `EARTHLY_NO_OUTPUT=true`.

Instructs EarthBuild not to output any images or artifacts. This option cannot be used with the *artifact form* or the *image form*.

##### `--output`

Also available as an env var setting: `EARTHLY_OUTPUT=true`.

Allow artifacts or images to be output, even when running under --ci mode.

##### `--no-cache`

Also available as an env var setting: `EARTHLY_NO_CACHE=true`.

Instructs EarthBuild to ignore any cache when building. It does, however, continue to store new cache formed as part of the build (to be possibly used on future invocations).

##### `--auto-skip` (**experimental**)

Also available as an env var setting: `EARTHLY_AUTO_SKIP=true`.

Instructs EarthBuild to skip any targets that have not changed from a previous build. For more information see the [auto-skip guide](../caching/caching-in-earthfiles.md#auto-skip).

##### `--allow-privileged|-P`

Also available as an env var setting: `EARTHLY_ALLOW_PRIVILEGED=true`.

Permits the build to use the --privileged flag in RUN commands. For more information see the [`RUN --privileged` command](../earthfile/earthfile.md#run).

##### `--ci`

Also available as an env var setting: `EARTHLY_CI=true`

In *target mode*, this option is an alias for

```
--no-output --strict
```

In *artifact* and *image modes* , this option is an alias for

```
--strict
```

##### `--platform <platform>`

Also available as an env var setting: `EARTHLY_PLATFORMS=<platform>`.

Sets the platform to build for.

{% hint style='info' %}
##### Note
It is not yet possible to specify multiple platforms through this flag. You may, however, use a wrapping target and a `BUILD` command in your Earthfile:

```Dockerfile
build-all-platforms:
  BUILD --platform=linux/amd64 --platform=linux/arm/v7 +build

build:
  ...
```
{% endhint %}

##### `--build-arg <key>[=<value>]` (**deprecated**)

This option has been deprecated in favor of the new build arg syntax `earth <target-ref> --<key>=<value>`.

Also available as an env var setting: `EARTHLY_BUILD_ARGS="<key>=<value>,<key>=<value>,..."`.

Overrides the value of the build arg `<key>`. If `<value>` is not specified, then the value becomes the value of the environment variable with the same name as `<key>`. For more information see the [`ARG` Earthfile command](../earthfile/earthfile.md#arg).

##### `--interactive|-i`

Also available as an env var setting: `EARTHLY_INTERACTIVE=true`.

Enable interactive debugging mode. By default when a `RUN` command fails, earth will display the error and exit. If the interactive mode is enabled and an error occurs, an interactive shell is presented which can be used for investigating the error interactively. Due to technical limitations, only a single interactive shell can be used on the system at any given time.

##### `--strict`

Disallow usage of features that may create unrepeatable builds.

#### Log formatting options

These options can only be set via environment variables, and have no command line equivalent.

| Variable               | Usage                                                                                                                                                                                                      |
|------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| NO_COLOR               | `NO_COLOR=1` disables the use of color.                                                                                                                                                                    |
| FORCE_COLOR            | `FORCE_COLOR=1` forces the use of color.                                                                                                                                                                   |
| EARTHLY_TARGET_PADDING | `EARTHLY_TARGET_PADDING=n` will set the column to the width of `n` characters. If a name is longer than `n`, its path will be truncated and remaining extra length will cause the column to go ragged. |
| EARTHLY_FULL_TARGET    | `EARTHLY_FULL_TARGET=1` will always print the full target name, and leave the target name column ragged.                                                                                                   |


## earth --version

#### Synopsis

- ```
  earth --version
  ```

#### Description

Prints version information about earth.

## earth ls

#### Synopsis

- ```
  earth ls [<earthfile-ref>]
  ```

#### Description

Prints all targets in an `Earthfile` in a project.

#### Options

##### `--args`

Show arguments (`ARG` statements) in the targets.

##### `--long`

Show full, canonical target references (includes the project part of the reference, if applicable).

## earth doc

#### Synopsis

- ```
  earth doc [<earthfile-ref>[+<target-ref>]]
  ```

#### Description

Prints documentation comments for documented targets in an `Earthfile` in a
project. Documentation on a target is any comment block that ends on the line
immediately above the target definition and begins with the name of the target.

#### Examples

Given the following `Earthfile`:

```
VERSION 0.8
FROM golang:1.19-alpine3.15

deps:
    COPY go.mod go.sum .
    RUN go mod download

# build runs 'go build' and saves the artifact locally.
build:
    FROM +deps
    COPY . .
    ARG output=./build/something
    RUN go build -o /bin/something
    SAVE ARTIFACT /bin/something AS LOCAL $output

# tidy runs 'go mod tidy' and saves go.mod/go.sum locally.
tidy:
    FROM +deps
    COPY . .
    RUN go mod tidy
    SAVE ARTIFACT go.mod AS LOCAL go.mod
    SAVE ARTIFACT go.sum AS LOCAL go.sum
```

##### Print the doc comments for all documented targets:

```
$ earth doc
TARGETS:
  +build
    build runs 'go build' and saves the artifact locally.
  +tidy
    tidy runs 'go mod tidy' and saves go.mod/go.sum locally.
```

Note that, unlike `earth ls`, `earth doc` does not mention the `deps`
target. Since it has no documentation, the `deps` target is not included in the
output.

##### Print the doc comments for a specific target:

```
$ earth doc +build
+build
  build runs 'go build' and saves the artifact locally.
```


## earth prune

#### Synopsis

- ```
  # Standard form
  earth [options] prune (--all|-a)

  # Reset form
  earth [options] prune --reset
  ```

#### Description

The command `earth prune` eliminates the EarthBuild cache.

In *standard form* (default) it issues a prune command to the BuildKit daemon.

In *reset form* it restarts the BuildKit daemon, instructing it to completely delete the cache directory on startup, thus forcing it to start from scratch.

#### Options

##### `--all|-a`

Instructs earth to issue a "prune all" command to the BuildKit daemon.

##### `--reset`

Restarts the BuildKit daemon and completely resets the cache directory.

##### `--age`

Prunes cache older than the specified duration. Accepts a duration string, which is a sequence of decimal numbers, each with optional fraction and a unit suffix, such as `300ms`. Valid time units are `ns`, `us`, `ms`, `s`, `m`, `h`.

##### `--size`

Prunes cache to specified size, starting with the oldest cache. It will eliminate cache until it reaches or exceeds the target size.

## earth config

#### Synopsis

```
# Set key value in your earth config

earth [options] config [key] [value]
```

#### Description

Manipulates values in `~/.earthly/config.yml`. It does its best to preserve existing formatting and comments. `[value]` must be a valid YAML literal for the given `[key]`.

#### Options

##### `--dry-run`

Prints the changed config file to the console instead of writing it to file


#### Examples

Set your cache size:

```
earth config global.cache_size_mb 1234
```

Set additional BuildKit args, using a YAML array:

```
earth config global.buildkit_additional_args ['userns', '--host']
```

Set a key containing a period:

```
earth config git."example.com".password hunter2
```

Set up a whole custom git repository for a server called example.com, using a single-line YAML literal:
- which stores git repos under /var/git/repos/name-of-repo.git
- allows access over ssh
- using port 2222
- sets the username to git
- is recognized to earth as example.com/name-of-repo

```
config git "{example: {pattern: 'example.com/([^/]+)', substitute: 'ssh://git@example.com:2222/var/git/repos/\$1.git', auth: ssh, user: git}}"
```

The above command yields the following config file:

```yaml
git:
    example:
        pattern: example.com/([^/]+)
        substitute: ssh://git@example.com:2222/var/git/repos/$1.git
        auth: ssh
        user: git
```

## earth bootstrap

#### Synopsis

- ```
  earth [options] bootstrap [--no-buildkit, --with-autocomplete, --certs-hostname]
  ```

#### Description

Performs initialization tasks needed for `earth` to function correctly. This command can be re-run to fix broken setups. It is recommended to run this with sudo.

#### Options

##### `--no-buildkit`

Skips setting up the BuildKit container during bootstrapping. If needed, it will also be performed when a build is ran.

##### `--with-autocomplete`

Installs shell autocompletions during bootstrap. Requires `sudo` to install them correctly.

##### `--certs-hostname <value>`

Takes in a value as the hostname for which to generate a TLS key/certificate pair

## earth docker-build

#### Synopsis

- ```
  earth [options] docker-build [--dockerfile <dockerfile-path>] [--tag=<image-tag>] [--target=<target-name>] [--platform <platform1[,platform2,...]>] <build-context-dir> [--arg1=arg-value]
  ```

#### Description

The command `earth docker-build` builds a docker image from a Dockerfile instead of an Earthfile.
The `<build-context-dir>` is the path where the Dockerfile build context exists. By default, it is assumed that a file named Dockerfile exists in that directory.

Additionally, all other build options are supported when using `docker-build`. For more information see [build-options](#build-options).

#### Examples

Build a dockerfile within the context of the `myDockerfiles` directory.

```
earth docker-build --dockerfile Dockerfile ./myDockerfiles
```

Push an image built from your Dockerfile built for linux/arm64

```
earth docker-build --dockerfile Dockerfile --platform linux/arm64 --tag {DOCKER_TAG} --push ./myDockerfiles
```

#### Options

##### `--dockerfile <dockerfile-path>`

Specify an alternative Dockerfile to use.

##### `--tag=<image-tag>`

Set the image name and tag to use. This option can be repeated to provide the built image multiple tags.

##### `--target=<target-name>`

Specifies the target to build in a multi-target Dockerfile.

##### `--platform <platform1[,platform2,...]>`

Sets the platform to build for.

{% hint style='info' %}
##### Note
Unlike a regular build command, it is possible to specify multiple platforms through this option.
{% endhint %}

