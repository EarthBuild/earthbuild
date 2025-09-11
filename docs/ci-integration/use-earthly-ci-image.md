# Building An Earthly CI Image

## Introduction

This guide is intended to help you use the Earthly image for your containerized CI workflows.

## Prerequisites

The `earthbuild/earthbuild` image requires that it is run as `--privileged`, or alternatively, it is run without the embedded BuildKit daemon (`NO_BUILDKIT=1`).

## Getting Started

Please see the reference documentation of the [`earthbuild/earthbuild` image on DockerHub](https://hub.docker.com/r/earthbuild/earthbuild).

It is recommended that the `earthbuild/earthbuild` image is used with a pinned version when used in the context of a CI, in order to avoid accidental future breakage as `earthly` evolves.

#### Using `/usr/bin/earthly-entrypoint.sh` as the entrypoint

The `earthbuild/earthbuild` image comes with an entrypoint that first starts up BuildKit and then issues an `earthly` command that makes use of it. You may use the image just as you would use `earthly` itself otherwise. Any arguments are passed into the `earthly` command directly.

{% hint style='danger' %}
##### Important
Note that using the `earthly` binary as the entrypoint will not start up BuildKit within the same container and will instead attempt to use the Docker Daemon (assuming one is available via `/var/run/docker.sock`) to start up BuildKit.
{% endhint %}

#### Remote Daemon

An alternative option is to use the `earthbuild/earthbuild` image in conjunction with a remote BuildKit Daemon. You may use the environment variable `BUILDKIT_HOST` to specify the hostname of the remote BuildKit Daemon. When this environment variable is set, the `earthbuild/earthbuild` image will not attempt to start BuildKit and will instead use the remote BuildKit Daemon.

For more details on using remote execution, [see our guide on remote BuildKit](./remote-buildkit.md).

#### Mounting the source code

The image expects the source code of the application you are building in the current working directory (by default `/workspace`). You will need to copy or mount the necessary files to that directory prior to invoking the entrypoint.

```bash
docker run --privileged --rm -v "$PWD":/workspace earthbuild/earthbuild:v0.8.13 +my-target
```

Or, if you would like to use an alternative directory:

```bash
docker run --privileged --rm -v "$PWD":/my-dir -w /my-dir earthbuild/earthbuild:v0.8.13 +my-target
```

Alternatively, you may rely on Earthly to perform a git clone, by using the remote target reference format. For example:

```bash
docker run --privileged --rm earthbuild/earthbuild:v0.8.13 github.com/foo/bar:my-branch+target
```

#### `NO_BUILDKIT` Environment Variable

As the embedded BuildKit daemon requires `--privileged`, for some operations you may be able to use the `NO_BUILDKIT=1` environment variable to disable the embedded BuildKit daemon. This is especially useful when running against a remote BuildKit.

## An important note about running the image

When running the built image in your CI of choice, if you're not using a remote daemon, Earthly will start BuildKit within the same container. In this case, it is important to ensure that the directory used by BuildKit to cache the builds is mounted as a Docker volume. Failing to do so may result in excessive disk usage, slow builds, or Earthly not functioning properly.

{% hint style='danger' %}
##### Important
We *strongly* recommend using a Docker volume for mounting `/tmp/earthly`. If you do not, BuildKit can consume excessive disk space, operate very slowly, or it might not function correctly.
{% endhint %}

In some environments, not mounting `/tmp/earthly` as a Docker volume results in the following error:

```
--> WITH DOCKER RUN --privileged ...
...
rm: can't remove '/var/earthbuild/dind/...': Resource busy
```

For more information, see the [documentation for `earthbuild/earthbuild` on DockerHub](https://hub.docker.com/r/earthbuild/earthbuild).
