In this section, we will explore how to use earthbuild in a CI system, such as GitHub Actions.

For more information on how to use earthbuild in other CIs such as GitLab, Jenkins, or CircleCI, you can check out the [CI Integration page](../ci-integration/overview.md).

## Using Earthbuild in Your Current CI

To use earthbuild in a CI, you typically encode the following steps in your CI's build configuration:

1. Download and install EarthBuild
2. Set up any credentials needed for the build
3. Log in to image registries, such as DockerHub
4. Run earthbuild

As part of this, you may need to set up credentials if you are using external secret management. For this, you can use the following command:

```bash
earthbuild account create-token my-ci-token
```

Finally, here is a complete example of how to run earthbuild in GitHub Actions:

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
      DOCKERHUB_USERNAME: ${{ secrets.DOCKERHUB_USERNAME }}
      DOCKERHUB_TOKEN: ${{ secrets.DOCKERHUB_TOKEN }}
      EARTHBUILD_TOKEN: ${{ secrets.EARTHBUILD_TOKEN }}
      FORCE_COLOR: 1
    steps:
    - uses: earthbuild/actions/setup-earthbuild@v1
      with:
        version: v0.8.16
    - uses: actions/checkout@v2
    - name: Docker Login
      run: docker login --username "$DOCKERHUB_USERNAME" --password "$DOCKERHUB_TOKEN"
    - name: Run build
      run: earthbuild --ci --push +build
```

Here is an explanation of the steps above:

* The action `earthbuild/actions/setup-earthbuild@v1` downloads and installs earthbuild. Running this action is similar to running the earthbuild installation one-liner `sudo /bin/sh -c 'wget https://github.com/earthbuild/earthbuild/releases/download/v0.8.16/earthbuild-linux-amd64 -O /usr/local/bin/earthbuild && chmod +x /usr/local/bin/earthbuild'`
* The command `docker login` performs a login to the DockerHub registry. This is required, to prevent rate-limiting issues when using popular base images.
* The command `earthbuild --ci --push +build` executes the build. The `--ci` flag is used here, in order to force the use of `--strict` mode. In `--strict` mode, EarthBuild prevents the use of features that make the build less repeatable and also disables local outputs -- because artifacts and images resulting from the build are not needed within the CI environment. Any outputs should be pushed via `RUN --push` or `SAVE IMAGE --push` commands. The build will be executed in the CI environment itself, with local caching capabilities.

For more information about integrating EarthBuild with other CI systems, you can check out the [CI Integration page](../ci-integration/overview.md).
