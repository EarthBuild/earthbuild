In this section, we will explore how to use EarthBuild in a CI system, such as GitHub Actions.

For more information on how to use EarthBuild in other CIs such as GitLab, Jenkins, or CircleCI, you can check out the [CI Integration page](../ci-integration/overview.md).

## Using EarthBuild in Your Current CI

To use EarthBuild in a CI, you typically encode the following steps in your CI's build configuration:

1. Download and install EarthBuild
2. Set up any credentials needed for the build
3. Log in to image registries, such as DockerHub
4. Run EarthBuild



Finally, here is a complete example of how to run EarthBuild in GitHub Actions:

```yaml
# .github/workflows/ci.yml

name: CI

on:
  push:
    branches: [ main ]
  pull_request:
    branches: [ main ]

jobs:
  build:
    runs-on: ubuntu-latest
    env:
      DOCKERHUB_USERNAME: ${{ vars.DOCKERHUB_USERNAME }}
      DOCKERHUB_TOKEN: ${{ secrets.DOCKERHUB_TOKEN }}
      FORCE_COLOR: 1
    steps:
    - uses: earthbuild/actions-setup@f4d20223e70dbb43b5fc08c4d857ab9cf0dbf3ae # v2.2.0
      with:
        version: v0.8.18
    - uses: actions/checkout@v4
    - name: Docker Login
      run: docker login --username "$DOCKERHUB_USERNAME" --password "$DOCKERHUB_TOKEN"
    - name: Run build
      run: earth --ci --push +build
```

Here is an explanation of the steps above:

- The action `earthbuild/actions-setup` downloads and installs EarthBuild. It is pinned to a commit SHA, with the version it corresponds to in a trailing comment; Renovate keeps both up to date. Running this action is similar to running the EarthBuild installation one-liner `sudo /bin/sh -c 'wget https://github.com/EarthBuild/earthbuild/releases/download/v0.8.18/earth-linux-amd64 -O /usr/local/bin/earth && chmod +x /usr/local/bin/earth'`
- The command `docker login` performs a login to the DockerHub registry. This is required, to prevent rate-limiting issues when using popular base images.
- The command `earth --ci --push +build` executes the build. The `--ci` flag is used here, in order to force the use of `--strict` mode. In `--strict` mode, EarthBuild prevents the use of features that make the build less repeatable and also disables local outputs -- because artifacts and images resulting from the build are not needed within the CI environment. Any outputs should be pushed via `RUN --push` or `SAVE IMAGE --push` commands. The build runs in the CI environment itself; to reuse a warm cache across runs, point EarthBuild at a self-hosted [remote runner](../ci-integration/remote-buildkit.md) with `--buildkit-host`.

For more information about integrating EarthBuild with other CI systems, you can check out the [CI Integration page](../ci-integration/overview.md).
