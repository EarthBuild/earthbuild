# Build Arguments and Variables

## Introduction

One of the core features of EarthBuild is support for build arguments. Build arguments are declared with `ARG` and
can be used to dynamically set environment variables inside the context of [RUN commands](../earthfile/earthfile.md#run).

Build arguments can be passed between targets or from the command line. They encourage
writing generic Earthfiles and ultimately promote greater code-reuse.

Another closely related primitive that EarthBuild offers is the variable (declared with `LET`). Variables are similar to build arguments, except that they cannot be used as parameters.

## A Quick Example

Arguments are declared either with the [ARG](../earthfile/earthfile.md#arg) keyword.

Let's consider a "hello world" example that allows us to change who is being greeted (e.g. hello banana, hello eggplant etc).
We will create a hello target that accepts the `name` argument:

```Dockerfile
VERSION 0.8
FROM alpine:latest

hello:
    ARG name
    RUN echo "hello $name"
```

Then we will specify a value for the `name` argument on the command line when we invoke `earth`:

```bash
earth +hello --name=world
```

This will output

```
    buildkitd | Found buildkit daemon as docker container (earth-buildkitd)
alpine:latest | --> Load metadata linux/arm64
         +foo | --> FROM alpine:latest
         +foo | 100% resolve docker.io/library/alpine:latest@sha256:21a3deaa0d32a8057914f36584b5288d2e5ecc984380bc0118285c70fa8c9300
         +foo | name=world
         +foo | --> RUN echo "hello $name"
         +foo | hello world
       output | --> exporting outputs
```

If we re-run `earth +hello --name=world`, we will see that the echo command is cached (and won't re-display the hello world text):

```
+foo | *cached* --> RUN echo "hello $name"
```

## Default values

Arguments may also have default values, which may be either constant or dynamic. For example, the following target will greet the name identified by the arg `name` (which has a default value of John), with the current time:

```Dockerfile
hello:
   ARG time=$(date +%H:%M)
   ARG name=John
   RUN echo "hello $name, it is $time"
```

```
alpine:latest | --> Load metadata linux/arm64
        +base | --> FROM alpine:latest
        +base | 100% resolve docker.io/library/alpine:latest@sha256:21a3deaa0d32a8057914f36584b5288d2e5ecc984380bc0118285c70fa8c9300
       +hello | --> ARG time = RUN $(date +%H:%M)
       +hello | --> RUN echo "hello $name, it is $time"
       +hello | hello John, it is 23:21
       output | --> exporting outputs
```

If an arg has no default value, then the default value is the empty string.

## Overriding Argument Values

Argument values can be set multiple ways:

1. On the command line

   The value can be directly specified on the command line (as shown in the previous example):
   
   ```
   earth +hello --HELLO=world --FOO=bar
   ```

2. From environment variables

   Similar to above, except that the value is an environment variable:
   
   ```bash
   export HELLO="world"
   export FOO="bar"
   earth +hello --HELLO="$HELLO" --FOO="$FOO"
   ```

3. Via the `EARTH_BUILD_ARGS` environment variable

    The value can also be set via the `EARTH_BUILD_ARGS` environment variable.
    
    ```bash
    export EARTH_BUILD_ARGS="HELLO=world,FOO=bar"
    earth +hello
    ```

    This may be useful if you have a set of build args that you'd like to always use and would prefer not to have to specify them on the command line every time. The `EARTH_BUILD_ARGS` environment variable may also be stored in your `~/.bashrc` file, or some other shell-specific startup script.

4. From an `.arg` file

   It is also possible to create an `.arg` file to contain the build arguments to pass
   to earth. First create an `.arg` file with:
   
   ```
   name=eggplant
   ```
   
   Then simply run earth:
   
   ```bash
   earth +hello
   ```

## Passing Argument values to targets

Build arguments can also be set when calling build targets.

```Dockerfile
greeting:
   BUILD +hello --name=world

hello:
    ARG name
    RUN echo "hello $name"
```

### How argument values propagate

`BUILD`, `COPY`, `FROM`, `WITH DOCKER --load` and the other commands that reference a target all pass arguments
in exactly the same way. `COPY +target/artifact` does not behave differently from `BUILD +target` in this regard.

The rules are:

1. **An argument is only visible in a target that declares it with `ARG`.** Passing `--name=world` to a target
   (from the command line or from another target) does not make `$name` available in that target unless the
   target itself contains `ARG name`.

2. **Overrides are passed on automatically to targets in the same Earthfile.** An argument override is a value set
   explicitly, either on the command line (`earth +greeting --name=world`) or in a target reference
   (`BUILD +hello --name=world`). Overrides travel with the build to every target referenced in the same Earthfile,
   and from there on to the targets those reference, even when an intermediate target does not declare the `ARG`
   itself. In the example below, `earth +greeting --name=world` prints `hello world`, although `+greeting` does not
   declare `name` and cannot read it:

   ```Dockerfile
   greeting:
      BUILD +hello
      # $name is empty here, because +greeting does not declare ARG name.
      RUN echo "greeting sees '$name'"

   hello:
      ARG name
      RUN echo "hello $name"
   ```

   The same applies with `COPY +hello/some-file ./` or `FROM +hello` instead of `BUILD +hello`.

3. **An argument's default value is not passed on.** Only override values propagate. If `+greeting` declared
   `ARG name=world` and `earth +greeting` was run without `--name`, then `+hello` would still see an empty `name`.
   To pass on the value of an `ARG` declared in the current target (whether it came from an override or from its
   default), either pass it explicitly or use `--pass-args` (see below).

4. **Overrides are not passed on automatically to other Earthfiles.** References to targets in another directory,
   in a remote repository or via `IMPORT` do not receive the caller's overrides. In order to pass arguments to
   other Earthfiles, you must explicitly pass the argument. For example:

   ```Dockerfile
   ARG name
   BUILD ./other+hello --name=$name
   ```

   Or you can use the `--pass-args` flag:

   ```Dockerfile
   BUILD --pass-args ./other+hello
   ```

   `--pass-args` passes all the overrides, plus the current value of every `ARG` declared so far by the calling
   target and every `ARG --global` of its Earthfile, including those that only have their default value. Builtin
   args are never passed. It works on `BUILD`, `COPY` and `FROM`, for targets both in the same Earthfile and in other
   Earthfiles.

An explicit value in the target reference always takes precedence. For example, with `BUILD +hello --name=banana`,
`+hello` sees `banana` even when `earth` was called with `--name=world`.

### Matrix builds

If multiple build arguments values are defined for the same argument name, EarthBuild will build the target for each value; this makes it easy to configure a "build matrix" within EarthBuild.

For example, we can create a new `greetings` target which calls `+hello` multiple times:

```dockerfile
greetings:
    BUILD +hello \
        --name=world \
        --name=banana \
        --name=eggplant
```

Then when we call `earth +greetings`, earth will call `+hello` three times:

```
     buildkitd | Found buildkit daemon as docker container (earth-buildkitd)
 alpine:latest | --> Load metadata linux/amd64
         +base | --> FROM alpine:latest
         +base | resolve docker.io/library/alpine:latest@sha256:69e70a79f2d41ab5d637de98c1e0b055206ba40a8145e7bddb55ccc04e13cf8f ... 100%
        +hello | name=banana
        +hello | --> RUN echo "hello $name"
        +hello | name=eggplant
        +hello | --> RUN echo "hello $name"
        +hello | name=world
        +hello | --> RUN echo "hello $name"
        +hello | hello banana
        +hello | hello eggplant
        +hello | hello world
        output | --> exporting outputs
```

In addition to the `BUILD` command, build args can also be used with `FROM`, `COPY`, `WITH DOCKER --load` and a number of other commands:

```Dockerfile
BUILD +hello --name=world
COPY (+hello/file.txt --name=world) ./
FROM +hello --name=world
WITH DOCKER --load=(+hello --name=world)
  ...
END
```

Another way to pass build args is by specifying a dynamic value, delimited by `$(...)`. For example, in the following, the value of the arg `name` will be set as the output of the shell command `echo world` (which, of course is simply `world`):

```Dockerfile
BUILD +hello --name=$(echo world)
```

## Documenting Build Arguments

You can document build arguments inline using the `--description` flag (introduced in EarthBuild v0.8.20) on the [`ARG`](../earthfile/earthfile.md#arg) command:

```Dockerfile
# build creates the application container.
build:
    ARG --description="Environment stage (dev, staging, prod)" ENV=prod
    ARG --required --description="Database connection URL" DB_URL

    FROM alpine:3.18
    RUN echo "Building for ${ENV} using database ${DB_URL}"
```

Argument descriptions are automatically displayed when querying targets with [`earth doc`](../earth-command/earth-command.md#earth-doc):

```bash
earth doc +build
```

Output:

```
+build
    build creates the application container.

  ARG                  DEFAULT  DESCRIPTION
  --DB_URL (required)           Database connection URL
  --ENV                prod     Environment stage (dev, staging, prod)
```

Descriptions can also be specified using a comment block directly above the `ARG` instruction whose first word matches the argument name:

```Dockerfile
# build creates the application container.
build:
    # DB_URL is the database connection URL.
    ARG --required DB_URL
```

When both `--description` and a doc comment are present on an `ARG`, the `--description` flag takes precedence.

## Variables

Variables are similar to build arguments, except that they cannot be used as parameters. You can think of variables as "private" build arguments (or local variables). To declare a variable, you can use the `LET` command.

Variables can also be mutated via the `SET` command. For example:

```Dockerfile
hello:
   LET name = "world"
   RUN echo "hello $name"
   SET name = "banana"
   RUN echo "hello $name"
```

This can be useful when you would like to decide on the value of a variable based on an `IF` condition, or if you would like to construct the value of the variable via a `FOR` loop.

For more information on `LET` see the [`LET` Earthfile reference](../earthfile/earthfile.md#let).
