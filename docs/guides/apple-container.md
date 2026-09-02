# Apple Container

[Apple Container](https://github.com/apple/container) is Apple's native container runtime for macOS on Apple Silicon. It is a lightweight, daemonless container engine that runs Linux containers via macOS virtualization.

EarthBuild supports Apple Container as a native container engine on macOS (`darwin/arm64`), allowing you to run builds and manage the BuildKit daemon without Docker Desktop or Podman.

## Prerequisites

- **macOS on Apple Silicon** (`arm64`, M-series chips).
- **Apple Container CLI** installed:
  ```bash
  brew install container
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

```
 1. Init 🚀
————————————————————————————————————————————————————————————————————————————————

           buildkitd | Starting buildkit daemon as an Apple Container (earth-buildkitd)...
           buildkitd | ...Done
```

## Features & Integration Details

### Rosetta 2 Translation
Apple Container runs containers with `--rosetta` enabled by default. This allows executing both `linux/arm64` and `linux/amd64` binaries within your build steps seamlessly using macOS Rosetta translation.

### Automatic Resource Sizing
On macOS, EarthBuild automatically probes host hardware and allocates **75% of system RAM** (minimum 4GB) and **all CPU cores** to the BuildKit daemon VM container, ensuring high performance without manual VM tuning.

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
container volume delete -f earth-cache
```
