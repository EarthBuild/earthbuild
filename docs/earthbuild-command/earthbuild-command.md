# earthbuild command reference

## earthbuild

#### Synopsis

* Target form
  ```
  earthbuild [options...] <target-ref> [build-args...]
  ```
* Artifact form
  ```
  earthbuild [options...] --artifact|-a <target-ref>/<artifact-path> [<dest-path>] [build-args...]
  ```
* Image form
  ```
  earthbuild [options...] --image <target-ref> [build-args...]
  ```

#### Description

The earthbuild command executes a build referenced by `<target-ref>` (*target form* and *image form*) or `<artifact-ref>` (*artifact form*). In the *target form*, the referenced target and its dependencies are built. In the *artifact form*, the referenced artifact and its dependencies are built, but only the specified artifact is output. The output path of the artifact can be optionally overridden by `<dest-path>`. In the *image form*, the image produced by the referenced target and its dependencies are built, but only the specified image is output.

If a BuildKit daemon has not already been started, and the option `--buildkit-host` is not specified, this command also starts up a container named `earthbuild-buildkitd` to act as a build daemon.

The execution has four phases:

* Init
* Build
* Push (optional - disabled by default)
* Local output (optional - enabled by default)

During the init phase the configuration is interpreted and the BuildKit daemon is started (if applicable). During the build phase, the referenced target and all its direct or indirect dependencies are executed. During the push phase, when enabled, earthbuild performs image pushes and it also runs `RUN --push` commands.  During the local output phase, all applicable artifacts with an `AS LOCAL` specification are written to the specified output location, and all applicable docker images are loaded onto the host's docker daemon.

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

  * Target form `earthbuild <target-ref> [--<build-arg-key>=<build-arg-value>...]`
  * Artifact form `earthbuild --artifact <target-ref>/<artifact-path> <dest-path> [--<build-arg-key>=<build-arg-value>...]`
  * Image form `earthbuild --image <target-ref> [--<build-arg-key>=<build-arg-value>...]`

Also available as an env var setting: `EARTHBUILD_BUILD_ARGS="<build-arg-key>=<build-arg-value>,<build-arg-key>=<build-arg-value>,..."`.

Build arg overrides may be specified as part of the earthbuild command. The value of the build arg `<build-arg-key>` is set to `<build-arg-value>`.

In the target and image forms the build args are passed after the target reference. For example `earthbuild +some-target --NAME=john --SPECIES=human`. In the artifact form, the build args are passed immediately after the artifact reference, however they are surrounded by parenthesis, similar to a [`COPY` command](../earthfile/earthfile.md#copy). For example `earthbuild --artifact +some-target/some-artifact ./dest/path --NAME=john --SPECIES=human`.

The build arg overrides only apply to the target being called directly and any other target referenced as part of the same Earthfile. Build arg overrides, will not apply to targets referenced from other directories or other repositories.

##### Storing values in the `.arg` File

Build args can also be specified using a `.arg` file, relative to the current working directory where `earthbuild` is executed from, using the syntax:

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
The directory used for loading the `.arg` file is the directory where `earthbuild` is called from and not necessarily the directory where the Earthfile is located in.
{% endhint %}

{% hint style='danger' %}
##### Important
The `.arg` file is meant for settings which are specific to the local environment the build executes in. These settings may cause inconsistencies in the way the build executes on different systems, leading to builds that are difficult to reproduce. Keep the contents of `.arg` files to a minimum to avoid such issues.
{% endhint %}

##### Additional Information

For more information about build args see the [`ARG` Earthfile command](../earthfile/earthfile.md#arg), and the [build args guide](../guides/build-args.md).

#### Environment Variables and .env File

Flag options can either be set on the command line, or by using an equivalent environment variable, as specified under the [options section](#options).

It is also possible to set these flag options in an `.env` file, relative to the current working directory where `earthbuild` is executed from, using the syntax:

```.env
<NAME_OF_ENV_VAR>=<value>
...
```

Each variable must be specified on a separate line, without any surrounding quotes. If quotes are included, they will become part of the value.
Lines beginning with `#` are treated as comments. Blank lines are allowed. Here is a simple example:

```.env
# Settings
EARTHBUILD_ALLOW_PRIVILEGED=true
EARTHBUILD_VERBOSE=true
```

### Global Options

##### `--help`

Prints help information about earthbuild.

###### Synopsis

* ```
  earthbuild --help
  ```
* ```
  earthbuild <command> --help
  ```

##### `--config <path>`

Also available as an env var setting: `EARTHBUILD_CONFIG=<path>`.

Overrides the earthbuild [configuration file](../earthbuild-config/earthbuild-config.md), defaults to `~/.earthbuild/config.yml`.

##### `--installation-name <name>`

Also available as an env var setting: `EARTHBUILD_INSTALLATION_NAME=<name>`.

Overrides the earthbuild installation name. The installation name is used for the BuildKit Daemon name, the cache volume name, the configuration directory (`~/.<installation-name>`) and for the ports used by BuildKit. Using multiple installation names on the same system allows EarthBuild to run as multiple isolated instances, each with its own configuration, cache and daemon. Defaults to `earthbuild`.

##### `--ssh-auth-sock <path-to-sock>`

Also available as an env var setting: `EARTHBUILD_SSH_AUTH_SOCK=<path-to-sock>`.

Sets the path to the SSH agent sock, which can be used for SSH authentication. SSH authentication is used by EarthBuild in order to perform git clone's underneath.

On Linux systems, this setting defaults to the value of the env var $SSH_AUTH_SOCK. On most systems, the env var `SSH_AUTH_SOCK` env var is already set if an SSH agent is running.

On Mac systems, this setting defaults to `/run/host-services/ssh-auth.sock` to match recommendation in [the official Docker documentation](https://docs.docker.com/docker-for-mac/osxfs/#ssh-agent-forwarding).

For more information see the [Authentication page](../guides/auth.md).

##### `--auth-token <value>`

Also available as an env var setting: `EARTHBUILD_TOKEN=<value>`.

Force earthbuild account login to authenticate with supplied token.

##### `--verbose`

Also available as an env var setting: `EARTHBUILD_VERBOSE=1`.

Enables verbose logging.

##### `--git-username <git-user>` (**deprecated**)

Also available as an env var setting: `GIT_USERNAME=<git-user>`.

This option is now deprecated. Please use the [configuration file](../earthbuild-config/earthbuild-config.md) instead.

##### `--git-password <git-pass>` (**deprecated**)

Also available as an env var setting: `GIT_PASSWORD=<git-pass>`.

This option is now deprecated. Please use the [configuration file](../earthbuild-config/earthbuild-config.md) instead.

##### `--git-url-instead-of <git-instead-of>` (**obsolete**)

Also used to be available as an env var setting: `GIT_URL_INSTEAD_OF=<git-instead-of>`.

This option is now obsolete. By default, `earthbuild` will automatically switch from ssh to HTTPS when no keys are found or the ssh-agent isn't running.
Please use the [configuration file](../earthbuild-config/earthbuild-config.md) to override the default behavior.

### Build Options

Build options are specific to executing earthbuild builds; they are simply listed in this section for readability, and can be supplied as global options.

##### `--secret|-s <secret-id>[=<value>]`

Also available as an env var setting: `EARTHBUILD_SECRETS="<secret-id>=<value>,<secret-id>=<value>,..."`.

Passes a secret with ID `<secret-id>` to the build environments. If `<value>` is not specified, then the value becomes the value of the environment variable with the same name as `<secret-id>`.

The secret can be referenced within Earthfile recipes as `RUN --secret <arbitrary-env-var-name>=<secret-id>`. For more information see the [`RUN --secret` Earthfile command](../earthfile/earthfile.md#run).

Secrets can also be stored in a `.secret` file using the same syntax as an `.arg` file; an example is given under the [secrets guide](../guides/secrets.md).

##### `--secret-file <secret-id>=<path>`

Also available as an env var setting: `EARTHBUILD_SECRET_FILES="<secret-id>=<path>,<secret-id>=<path>,..."`.

Loads the contents of a file located at `<path>` into a secret with ID `<secret-id>` for use within the build environments.

The secret can be referenced within Earthfile recipes as `RUN --secret <arbitrary-env-var-name>=<secret-id>`. For more information see the [`RUN --secret` Earthfile command](../earthfile/earthfile.md#run).

##### `--push`

Also available as an env var setting: `EARTHLY_PUSH=true`.

Instructs earthbuild to push any docker images declared with the `--push` flag to remote docker registries and to run any `RUN --push` commands. For more information see the [`SAVE IMAGE` Earthfile command](../earthfile/earthfile.md#save-image) and the [`RUN --push` Earthfile command](../earthfile/earthfile.md#run).

Pushing only happens during the output phase, and only if the build has succeeded.

##### `--no-output`

Also available as an env var setting: `EARTHBUILD_NO_OUTPUT=true`.

Instructs earthbuild not to output any images or artifacts. This option cannot be used with the *artifact form* or the *image form*.

##### `--output`

Also available as an env var setting: `EARTHBUILD_OUTPUT=true`.

Allow artifacts or images to be output, even when running under --ci mode.

##### `--no-cache`

Also available as an env var setting: `earthbuild_NO_CACHE=true`.

Instructs earthbuild to ignore any cache when building. It does, however, continue to store new cache formed as part of the build (to be possibly used on future invocations).

##### `--auto-skip` (**experimental**)

Also available as an env var setting: `earthbuild_AUTO_SKIP=true`.

Instructs earthbuild to skip any targets that have not changed from a previous build. For more information see the [auto-skip guide](../caching/caching-in-earthfiles.md#auto-skip).

##### `--allow-privileged|-P`

Also available as an env var setting: `EARTHBUILD_ALLOW_PRIVILEGED=true`.

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

Also available as an env var setting: `earthbuild_PLATFORMS=<platform>`.

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

This option has been deprecated in favor of the new build arg syntax `earthbuild <target-ref> --<key>=<value>`.

Also available as an env var setting: `EARTHBUILD_BUILD_ARGS="<key>=<value>,<key>=<value>,..."`.

Overrides the value of the build arg `<key>`. If `<value>` is not specified, then the value becomes the value of the environment variable with the same name as `<key>`. For more information see the [`ARG` Earthfile command](../earthfile/earthfile.md#arg).

##### `--interactive|-i`

Also available as an env var setting: `earthbuild_INTERACTIVE=true`.

Enable interactive debugging mode. By default when a `RUN` command fails, earthbuild will display the error and exit. If the interactive mode is enabled and an error occurs, an interactive shell is presented which can be used for investigating the error interactively. Due to technical limitations, only a single interactive shell can be used on the system at any given time.

##### `--strict`

Disallow usage of features that may create unrepeatable builds.

#### Log formatting options

These options can only be set via environment variables, and have no command line equivalent.

| Variable               | Usage                                                                                                                                                                                                      |
|------------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| NO_COLOR               | `NO_COLOR=1` disables the use of color.                                                                                                                                                                    |
| FORCE_COLOR            | `FORCE_COLOR=1` forces the use of color.                                                                                                                                                                   |
| earthbuild_TARGET_PADDING | `earthbuild_TARGET_PADDING=n` will set the column to the width of `n` characters. If a name is longer than `n`, its path will be truncated and remaining extra length will cause the column to go ragged. |
| earthbuild_FULL_TARGET    | `earthbuild_FULL_TARGET=1` will always print the full target name, and leave the target name column ragged.                                                                                                   |


## earthbuild --version

#### Synopsis

* ```
  earthbuild --version
  ```

#### Description

Prints version information about earthbuild.

## earthbuild ls

#### Synopsis

* ```
  earthbuild ls [<earthfile-ref>]
  ```

#### Description

Prints all targets in an `Earthfile` in a project.

#### Options

##### `--args`

Show arguments (`ARG` statements) in the targets.

##### `--long`

Show full, canonical target references (includes the project part of the reference, if applicable).

## earthbuild doc

#### Synopsis

* ```
  earthbuild doc [<earthfile-ref>[+<target-ref>]]
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
$ earthbuild doc
TARGETS:
  +build
    build runs 'go build' and saves the artifact locally.
  +tidy
    tidy runs 'go mod tidy' and saves go.mod/go.sum locally.
```

Note that, unlike `earthbuild ls`, `earthbuild doc` does not mention the `deps`
target. Since it has no documentation, the `deps` target is not included in the
output.

##### Print the doc comments for a specific target:

```
$ earthbuild doc +build
+build
  build runs 'go build' and saves the artifact locally.
```


## earthbuild prune

#### Synopsis

* ```
  # Standard form
  earthbuild [options] prune (--all|-a)

  # Reset form
  earthbuild [options] prune --reset
  ```

#### Description

The command `earthbuild prune` eliminates the earthbuild cache.

In *standard form* (default) it issues a prune command to the BuildKit daemon.

In *reset form* it restarts the BuildKit daemon, instructing it to completely delete the cache directory on startup, thus forcing it to start from scratch.

#### Options

##### `--all|-a`

Instructs earthbuild to issue a "prune all" command to the BuildKit daemon.

##### `--reset`

Restarts the BuildKit daemon and completely resets the cache directory.

##### `--age`

Prunes cache older than the specified duration. Accepts a duration string, which is a sequence of decimal numbers, each with optional fraction and a unit suffix, such as `300ms`. Valid time units are `ns`, `us`, `ms`, `s`, `m`, `h`.

##### `--size`

Prunes cache to specified size, starting with the oldest cache. It will eliminate cache until it reaches or exceeds the target size.

## earthbuild config

#### Synopsis

```
# Set key value in your earthbuild config

earthbuild [options] config [key] [value]
```

#### Description

Manipulates values in `~/.earthbuild/config.yml`. It does its best to preserve existing formatting and comments. `[value]` must be a valid YAML literal for the given `[key]`.

#### Options

##### `--dry-run`

Prints the changed config file to the console instead of writing it to file


#### Examples

Set your cache size:

```
earthbuild config global.cache_size_mb 1234
```

Set additional BuildKit args, using a YAML array:

```
earthbuild config global.buildkit_additional_args ['userns', '--host']
```

Set a key containing a period:

```
earthbuild config git."example.com".password hunter2
```

Set up a whole custom git repository for a server called example.com, using a single-line YAML literal:
* which stores git repos under /var/git/repos/name-of-repo.git
* allows access over ssh
* using port 2222
* sets the username to git
* is recognized to earthbuild as example.com/name-of-repo

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

## earthbuild account

Contains sub-commands for registering and administration an earthbuild account.

### earthbuild account register

#### Synopsis


* ```
  # Register an account using your email
  earthbuild [options] account register --email <email>

  # Complete account registration
  earthbuild [options] account register --email <email> --token <email-verification-token> [--password <password>] [--public-key <public-key>] [--accept-terms-conditions-privacy]
  ```

#### Description

Register for an earthbuild account. Registration is done in two steps: first run the register command with only the --email argument, this will then send an email to the
supplied email address with a registration token (which is used to verify your email address), second re-run the register command with both the --email and --token arguments
to complete the registration process.

#### Options

##### `--email <email>`

Pass in an email address for registering your earthbuild account. An email will be sent containing your registration token.

##### `--token <token>`

Pass in token for email verification. Retrieve the token from your email and register it with the `--email` option.

##### `--password <password>`

Specify your password on the command line instead of interactively being asked.

##### `--public-key <public-key>`

Path to public key to register.

##### `--accept-terms-of-service-privacy`

Accept the Terms & Conditions, and Privacy Policy.

### earthbuild account login

#### Synopsis

* ```
  # login using registered public keys or check who you are logged in as
  earthbuild [options] account login

  # Login with email and input password interactively
  earthbuild [options] account login --email <email>

  # Login with email and password
  earthbuild [options] account login --email <email> --password <password>

  # Login with your tokem
  earthbuild [options] account login --token <token>
  ```

#### Description

Login to an existing earthbuild account. If no email or token is given, earthbuild will attempt to login using [registered public keys](../public-key-auth/public-key-auth.md).

#### Options

##### `--email <email>`

Pass in email address connected with your earthbuild account.

##### `--token <token>`

Pass in your authentication token

##### `--password <password>`

Pass in the password for your earthbuild account. If not provided you will be interactively asked.

### earthbuild account logout

#### Synopsis

* ```
  earthbuild [options] account logout
  ```

#### Description

Removes cached login information from `~/.earthbuild/auth.token`.

### earthbuild account list-keys

#### Synopsis

* ```
  earthbuild [options] account list-keys
  ```

#### Description

Lists all public keys that are authorized to login to the current earthbuild account.

### earthbuild account add-key

#### Synopsis

* ```
  earthbuild [options] account add-key [<public-key>]
  ```

#### Description

Authorize a new public key to login to the current earthbuild account. If `key` is omitted, an interactive prompt is displayed to select a public key to add.

### earthbuild account remove-key

#### Synopsis

* ```
  earthbuild [options] account remove-key <public-key>
  ```

#### Description

Removes an authorized public key from accessing the current earthbuild account.

### earthbuild account list-tokens

#### Synopsis

* ```
  earthbuild [options] account list-tokens
  ```

#### Description

List account tokens associated with the current earthbuild account. A token is useful for environments where the ssh-agent is not accessible (e.g. a CI system).

### earthbuild account create-token

#### Synopsis

* ```
  earthbuild [options] account create-token [--write] [--expiry <expiry>] [--overwrite] <token-name>
  ```

#### Description

Creates a new authentication token. A read-only token is created by default, If the `--write` flag is specified the token will have read+write access.
The token will never expire unless a different date is supplied via the `--expiry` flag.
If the token by the same name already exists, it will not be overwritten unless the `--overwrite` flag is specified.

{% hint style='info' %}
It is then possible to `export EARTHBUILD_TOKEN=...`, which will force earthbuild to use this token for all authentication (overriding any other currently-logged in sessions).
{% endhint %}

#### Options

##### `--write`

Grant write permissions in addition to read permissions

##### `--expiry`

Set token expiry date in the form YYYY-MM-DD or never

##### `--overwrite`

Overwrite the token if it already exists

### earthbuild account remove-token

#### Synopsis

* ```
  earthbuild [options] account remove-token <token>
  ```

#### Description

Removes a token from the current earthbuild account.

### earthbuild account reset

#### Synopsis

* ```
  earthbuild [options] account reset --email <email> [--token <token>]
  ```

#### Description

Reset the password associated with the provided email. The command should first be run without a token, which will cause a token to be emailed to you. Once the command is re-run with the provided token, it will prompt you for a new password.

#### Options

##### `--email <email>`

Email address for which to reset the password.

##### `--token <token>`

Authentication token with with to rerun the command with your email to reset your password. Once run you will be prompted for a new password.

## earthbuild org

Contains sub-commands for creating and managing earthbuild organizations.

### earthbuild org create

#### Synopsis

* ```
  earthbuild [options] org create <org-name>
  ```

#### Description

Create a new organization, which can be used to share secrets between different user accounts.

### earthbuild org list

#### Synopsis

* ```
  earthbuild [options] org list

  earthbuild [options] org ls
  ```

#### Description

List all organizations the current account is a member, or administrator of.

### earthbuild org list-permissions

#### Synopsis

* ```
  earthbuild [options] org list-permissions <org-name>
  ```

#### Description

List all accounts and the paths they have permission to access under a particular organization.

### earthbuild org invite

#### Synopsis

* ```
  earthbuild [options] org [--org <organization-name>] invite [--name <recipient-name>] [--permission <permission>] [--message <message>] <email>
  ```

#### Description

Invites a user into an organization; `<org-path>` can either be a top-level org access by granting permission on `/<org-name>/`, or finer-grained access can be granted to a subpath e.g. `/<org-name>/path/to/share/`.
By default users are granted read-only access unless the `--write` flag is given.

#### Subcommands

##### `accept`

Accept an invitation to join an organization

##### `ls | list`

List all sent invitations (both pending and accepted)

#### Options

##### `--permission`

The access level the new organization member will have. Can be one of: read, write, or admin.

##### `--message`

An optional message to send with the invitation email

### earthbuild org revoke

#### Synopsis

* ```
  earthbuild [options] org revoke <org-path> <email> [<email>, ...]
  ```

#### Description

Revokes a previously invited user from an organization.

### earthbuild org member

#### Synopsis

* ```
  earthbuild [options] org [--org <organization-name>] members (ls|update|rm)
  ```

#### Description

Manage organization members

#### Subcommands

##### `ls`

List organization members and their permission level

##### `update`

Update an organization member's permissions.

###### `--permission`

Flag for `update` subcommand. Can be one of: read, write, or admin.

##### `rm`

Remove a user from the organization

### earthbuild org select

#### Synopsis

* ```
  earthbuild [options] org select <org-name>
  ```

#### Description

Selects an existing earthbuild org to be the default. Analogous to the `EARTHBUILD_ORG` environment variable, or the `--org` flag available on some commands. When multiple organizations are specified, the precedence order is the following:

1. `--org` argument
2. `EARTHBUILD_ORG` environment variable
3. The configuration setting controlled by this command

### earthbuild org unselect

#### Synopsis

* ```
  earthbuild [options] org unselect
  ```

#### Description

Removes the configuration option specifying a default organization.

## earthbuild secrets

#### Synopsis

Alias `earthbuild secret`

* ```
  earthbuild [options] secrets [--org <organization-name>, --project <project>] (set|get|ls|rm|migrate|permission)
  ```
Contains sub-commands for creating and managing earthbuild secrets.

#### Description

Contains sub-commands for creating and managing earthbuild secrets.

#### Options

##### `--org`

The organization to which the project belongs.

##### `--project`

The organization project in which to store secrets.

### earthbuild secrets set

#### Synopsis

* ```
  earthbuild [options] secrets set <path> <value>
  earthbuild [options] secrets set --file <local-path> <path>
  ```

#### Description

Stores a secret in the secrets store.

#### Options

##### `--file`

Stores secret from file to the path.

##### `--stdin`

Stores secret read from stdin to the path.

### earthbuild secrets get

#### Synopsis

* ```
  earthbuild [options] secrets get [-n] <path>
  ```

#### Description

Retrieve a secret from the secrets store. If `-n` is given, no newline is printed after the contents of the secret.

#### Options

##### `--n`

Disables newline at the end of the stored secret.

### earthbuild secrets ls

#### Synopsis

* ```
  earthbuild [options] secrets ls [<path>]
  ```

#### Description

List secrets the current account has access to.

### earthbuild secrets rm

#### Synopsis

* ```
  earthbuild [options] secrets rm <path>
  ```

#### Description

Removes a secret from the secrets store.

### earthbuild secrets migrate

#### Synopsis

* ```
  earthbuild [options] secrets --org <organization> --project <project> migrate <source-organization>
  ```

#### Description

Migrate existing secrets into the new project-based structure.

#### Options

##### `--dry-run`

Output what the command will do without actually doing it.

### earthbuild secrets permission

#### Synopsis

* ```
  earthbuild [options] secrets permission (ls|set|rm)
  ```

#### Description

Manage user-level secret permissions.

#### Subcommands

##### `ls`

List any user secret permissions.

##### `rm`

Remove a user secret permission.

##### `set`

Create or update a user secret permission.

## earthbuild registry

#### Synopsis

* ```
  earthbuild [options] registry [--org <organization-name>, --project <project>] (setup|list|remove) [<flags>]
  ```

#### Description

Contains sub-commands for managing registry access in cloud-based secrets.

#### Options

##### `--org`

The organization to store the credentials under; must be used in combination with `--project`. If omitted, the user's personal secret store will be used instead.

##### `--project`

The organization's project to store the credentials under; the user's secret store will be used if empty.

### earthbuild registry setup

#### Synopsis

* ```
  earthbuild [options] registry [--org <org> --project <project>] setup [--cred-helper <none|ecr-login|gcloud>] ...
  ```

#### Description

Store registry credentials in the earthbuild-cloud secrets store. These credentials are used to authenticate with the registry.
When they are associated with a project, by specifying `--org`, and `--project` flags, they will be associated with the project (as referenced by the
`PROJECT` Earthfile command), which is used when running in CI.

{% hint style='info' %}
##### Note
Registry credentials are stored under `std/registry/<host>/...` of either the user, or project based secrets.

The `earthbuild registry ...` commands exist for convience; however, it is possible to set (or delete) these values using the `earthbuild secrets ...` commands.
{% endhint %}


#### Examples
##### username/password based registry (`--cred-helper=none`)

* ```
  earthbuild [options] registry setup --username <username> --password <password> [<host>]

  earthbuild [options] registry --org <org> --project <project> setup --username <username> --password <password> [<host>]
  ```

##### AWS elastic container registry (`--cred-helper=ecr-login`)

* ```
  earthbuild registry setup --cred-helper ecr-login --aws-access-key-id <key> --aws-secret-access-key <secret> <host>

  earthbuild registry --org <org> --project <project> setup --cred-helper ecr-login --aws-access-key-id <key> --aws-secret-access-key <secret> <host>
  ```

##### GCP artifact or container registry (`--cred-helper=gcloud`)

* ```
  earthbuild registry setup --cred-helper gcloud --gcp-key <key> <host>

  earthbuild registry --org <org> --project setup <project> --cred-helper gcloud --gcp-service-account-key <key> <host>
  ```

#### Options

##### `--cred-helper`

When specified, use a credential helper for authenticating with the registry. Values can be `ecr-login`, `gcloud`, or `none`.

Also available as an env var setting: `earthbuild_REGISTRY_CRED_HELPER=<value>`.

##### `--username <username>`

The username to use; only applicable when `--cred-helper` is omitted (or `none`).

Also available as an env var setting: `earthbuild_REGISTRY_USERNAME=<value>`.

##### `--password <password>`

The password to use; only applicable when `--cred-helper` is omitted (or `none`).

Also available as an env var setting: `earthbuild_REGISTRY_PASSWORD=<value>`.

##### `--password-stdin`

When set, read the password from stdin; only applicable when `--cred-helper` is omitted (or `none`).

Also available as an env var setting: earthbuild_REGISTRY_PASSWORD_STDIN=true.

##### `--aws-access-key-id <identifier>`

The AWS access key ID to use when requesting a registry token, only applicable when `--cred-helper=ecr-login`.

Also available as an env var setting: `AWS_ACCESS_KEY_ID=<identifier>`.

##### `--aws-secret-access-key <secret>`

The AWS secret access key to use when requesting a registry token, only applicable when `--cred-helper=ecr-login`.

Also available as an env var setting: `AWS_SECRET_ACCESS_KEY=<secret>`.

##### `--gcp-service-account-key <key>`

The GCP service account key to use when requesting a registry token, only applicable when `--cred-helper=gcloud`.

Also available as an env var setting: `GCP_SERVICE_ACCOUNT_KEY=<key>`.

##### `--gcp-service-account-key-path <path>`

Similar to `--gcp-service-account-key`, but read the key from the specified file.

Also available as an env var setting: `GCP_SERVICE_ACCOUNT_KEY_PATH=<path>`, or `GOOGLE_APPLICATION_CREDENTIALS=<path>`.

##### `--gcp-service-account-key-stdin`

Similar to `--gcp-service-account-key`, but read the key from stdin.

Also available as an env var setting: `GCP_SERVICE_ACCOUNT_KEY_PATH_STDIN=true`.

### earthbuild registry list

##### Synopsis

* ```
  earthbuild [options] registry list [--org <org> --project <project>]
  ```

##### Description

Display the configured registries.

### earthbuild registry remove

##### Synopsis

* ```
  earthbuild [options] registry remove [--org <org> --project <project>] <host>
  ```

##### Description

Remove a configured registry, and delete all stored credentials.

## earthbuild bootstrap

#### Synopsis

* ```
  earthbuild [options] bootstrap [--no-buildkit, --with-autocomplete, --certs-hostname]
  ```

#### Description

Performs initialization tasks needed for `earthbuild` to function correctly. This command can be re-run to fix broken setups. It is recommended to run this with sudo.

#### Options

##### `--no-buildkit`

Skips setting up the BuildKit container during bootstrapping. If needed, it will also be performed when a build is ran.

##### `--with-autocomplete`

Installs shell autocompletions during bootstrap. Requires `sudo` to install them correctly.

##### `--certs-hostname <value>`

Takes in a value as the hostname for which to generate a TLS key/certificate pair

## earthbuild web

#### Synopsis

* ```
  earthbuild [options] web [--provider=<provider-ref>]]
  ```

#### Description

Prints a url for entering the CI application and attempts to open your default browser with that url.
If the provider argument is given the CI application will automatically begin an OAuth flow with the given provider.
If you are logged into the CLI the url will contain a token used to link your OAuth credentials to your earthbuild user.

#### Options

##### `--provider`

The provider to use when logging into the web ui.

#### Examples

##### Login to the CI application with GitHub

* ```
  earthbuild web --provider=github
  ```

## earthbuild docker-build

#### Synopsis

* ```
  earthbuild [options] docker-build [--dockerfile <dockerfile-path>] [--tag=<image-tag>] [--target=<target-name>] [--platform <platform1[,platform2,...]>] <build-context-dir> [--arg1=arg-value]
  ```

#### Description

The command `earthbuild docker-build` builds a docker image from a Dockerfile instead of an Earthfile.
The `<build-context-dir>` is the path where the Dockerfile build context exists. By default, it is assumed that a file named Dockerfile exists in that directory.

Just like a regular build, `docker-build` can be used with remote runners. For example:
```shell
earthbuild docker-build --tag my-image:latest .
```
For more information see the [remote BuildKit guide](../ci-integration/remote-buildkit.md).

Additionally, all other build options are supported when using `docker-build`. For more information see [build-options](#build-options).

#### Examples

Build a dockerfile within the context of the `myDockerfiles` directory.

```
earthbuild docker-build --dockerfile Dockerfile ./myDockerfiles
```

Push an image built from your Dockerfile built for linux/arm64

```
earthbuild docker-build --dockerfile Dockerfile --platform linux/arm64 --tag {DOCKER_TAG} --push ./myDockerfiles
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



## earthbuild project

#### Synopsis

* ```
  earthbuild [options] project (ls|rm|create|member)
  ```

#### Description

Manage earthbuild projects which are shared resources of earthbuild orgs. Within earthbuild projects users can be invited and granted different access levels including: read, read+secrets, write, and admin.

#### Options

##### `--org`

The name of the organization to which the project belongs. Required when user is a member of multiple.

##### `--project`

The earthbuild project to act on.

### earthbuild project ls

#### Synopsis

* ```
  earthbuild [options] project [--org <organization-name>] ls
  ```

#### Description

List all projects that belong to the specified organization.

### earthbuild project create

#### Synopsis

* ```
  earthbuild [options] project [--org <organization-name>] create <project-name>
  ```

#### Description

Create a new project in the specified organization.

### earthbuild project rm

#### Synopsis

* ```
  earthbuild [options] project [--org <organization-name>] rm
  ```

#### Description

Remove an existing project and all of its associated resources.

#### Options

##### `--force`

Force removal without asking permission.

### earthbuild project member

#### Synopsis

* ```
  earthbuild [options] project [--org <organization-name>] member (ls|rm|add|update)
  ```

#### Description

Manage project members.

#### Subcommands

##### `add`

Add a new member to the specified project.

###### Synopsis

* ```
  earthbuild [options] project [--org <organization-name>] --project <project-name> member add <user-email> <permission>
  ```

##### `rm`

Remove a member from the specified project.

###### Synopsis

* ```
  earthbuild [options] project [--org <organization-name>] --project <project-name> member rm <user-email>
  ```

##### `ls`

List all members in the specified project.

###### Synopsis

* ```
  earthbuild [options] project [--org <organization-name>] --project <project-name> member ls
  ```

##### `update`

Update the project member's permission.

###### Synopsis

* ```
  earthbuild [options] project [--org <organization-name>] --project <project-name> member update <user-email> <permission>
  ```
