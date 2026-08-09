# EarthBuild lib

The EarthBuild [lib](https://github.com/EarthBuild/lib) is a collection of reusable functions for common operations to be used in Earthfiles.

EarthBuild lib is split across multiple packages, each of which can be imported separately. The packages are:

- [billing](https://github.com/EarthBuild/lib/tree/main/billing) - functions for estimating billing
- [rust](https://github.com/EarthBuild/lib/tree/main/rust) - functions for working with Rust
- [utils/dind](https://github.com/EarthBuild/lib/tree/main/utils/dind) - functions for working with Docker-in-Docker
- [utils/git](https://github.com/EarthBuild/lib/tree/main/utils/git) - functions for working with Git
- [utils/ssh](https://github.com/EarthBuild/lib/tree/main/utils/ssh) - functions for working with SSH

See the individual packages for more information.

Additional language-specific functions are currently planned for later in 2024.

## Usage

To use EarthBuild lib, import the package you want to use in your Earthfile:

```dockerfile
IMPORT github.com/EarthBuild/lib/utils/git:3.0.4 AS git
```

Then, call the function you want to use:

```dockerfile
DO git+DEEP_CLONE --GIT_URL=...
```
