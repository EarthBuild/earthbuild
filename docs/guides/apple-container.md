# Apple Container

[Apple Container](https://github.com/apple/container) is Apple's native container runtime for macOS on Apple Silicon. It is a lightweight, daemonless container engine that runs Linux containers via macOS virtualization.

EarthBuild supports Apple Container as a native container engine on macOS (`darwin/arm64`), allowing you to run builds and manage the BuildKit daemon without Docker Desktop or Podman.

## Prerequisites

- **macOS on Apple Silicon** (`arm64`, M-series chips, macOS 26+).
- **Apple Container CLI 1.2.1 or later** installed (BuildKit runs privileged, and `--read-only-path`/`--masked-path` first appeared in 1.2.1; EarthBuild refuses an older CLI by name):

  Install the signed package from [apple/container releases](https://github.com/apple/container/releases). The Homebrew formula lags behind - it was still 0.9.0 when 1.4.1 shipped - so `brew install container` can install a CLI EarthBuild refuses. Check with:

  ```bash
  container --version
  ```

- **Start the container system service**:

  ```bash
  container system start --enable-kernel-install
  ```

## Getting started

When `earth` starts, it automatically detects available container engines in this order:

1. **Docker**
2. **Podman**
3. **Apple Container**

If Docker is not running and Apple Container is available, EarthBuild will automatically use Apple Container.

To explicitly configure EarthBuild to always use Apple Container:

```bash
earth config global.container_frontend apple-container
```

You can verify the configuration in your `~/.earth/config.yml` file:

```yaml
global:
  container_frontend: apple-container
```

Then, run a build to verify the integration:

```bash
earth github.com/EarthBuild/hello-world:main+hello
```

You should see BuildKit start up inside Apple Container:

```text
 1. Init 🚀
————————————————————————————————————————————————————————————————————————————————

           buildkitd | Starting buildkit daemon as Apple Container (earth-buildkitd)...
           buildkitd | ...Done
```

## Features & Integration Details

### Rosetta 2 Translation

Apple Container runs containers with `--rosetta` enabled by default. This allows executing both `linux/arm64` and `linux/amd64` binaries within your build steps seamlessly using macOS Rosetta translation.

### Automatic Resource Sizing

On macOS, EarthBuild gives the BuildKit VM **half of system RAM** (minimum 16GB) and **all CPU cores**. Every build step runs inside that one VM, so its memory limit is the whole build's: a compile that exceeds it is killed by the VM's kernel, and the build reports only that the compiler was "terminated by a deadly signal". The figure is a ceiling, not a reservation - an idle VM holds about half a gigabyte - so the minimum costs nothing until a build uses it.

### Idle Shutdown and Memory

Apple Container does not return memory from a running VM to macOS: memory a build used stays with the VM until the VM stops, even after the build has finished and freed it. So BuildKit stops itself after **30 minutes with no client**, which stops the VM and gives its memory back. The cache is kept on the `earth-cache` volume, so the next build restarts BuildKit and rebuilds nothing.

Set [`buildkit_idle_timeout_s`](../earth-config/earth-config.md#buildkit_idle_timeout_s) to change the wait, or to `0` to keep BuildKit running. To give memory back immediately, stop it with `container stop earth-buildkitd`.

## Troubleshooting

### "container system service is not running"

Ensure the Apple Container service is running:

```bash
container system status
```

If it is stopped, start it with:

```bash
container system start --enable-kernel-install
```

### Checking Container Logs

If the daemon fails to start, inspect the container logs:

```bash
container logs earth-buildkitd
```

### Cleaning Up

To reset and remove the EarthBuild daemon and cache volume in Apple Container:

```bash
container delete -f earth-buildkitd
container volume delete earth-cache
```
