
# GitHub Actions integration

Here is an example of a GitHub Actions build that uses [earthbuild/actions-setup](https://github.com/EarthBuild/actions-setup).

This example assumes an [Earthfile](../../earthfile/earthfile.md) exists with a `+build` target:

```yml
# .github/workflows/ci.yml

name: EarthBuild +build

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

Alternatively, you can skip using the `earthbuild/actions-setup` job and include
a step to download `earth` instead:

```yml
# .github/workflows/ci.yml

name: EarthBuild +build

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
    - uses: actions/checkout@v4
    - name: Docker Login
      run: docker login --username "$DOCKERHUB_USERNAME" --password "$DOCKERHUB_TOKEN"
    - name: Download earth
      run: "sudo /bin/sh -c 'wget https://github.com/EarthBuild/earthbuild/releases/download/v0.8.18/earth-linux-amd64 -O /usr/local/bin/earth && chmod +x /usr/local/bin/earth'"
    - name: Run build
      run: earth --ci --push +build
```

For a complete guide on CI integration see the [CI integration guide](../overview.md).

## Diagnosing failures

`Canceled`, `context canceled`, `file already closed` and a lost solve session
are symptoms, not causes. They report that the build stopped, not why. A
download dying mid-transfer, one target failing and cancelling its siblings, a
daemon crash, and the runner's OOM killer taking a `buildkit-runc` child are
all capable of producing the same message, and which of them you are looking at
depends entirely on your build. Read the message as a prompt to investigate,
not as a diagnosis.

The `failure-diagnostics` action collects the evidence that discriminates
between them: the buildkit daemon log, memory and swap, and kernel OOM
messages. The memory side matters disproportionately because it is the one
cause that leaves no trace in the job log at all - an OOM kill is silent.

```yml
    - name: Run build
      run: earth --ci --push +build
    - name: Failure diagnostics
      if: ${{ failure() }}
      uses: EarthBuild/earthbuild/.github/actions/failure-diagnostics@main
```

Every input has a default, so that is the whole integration. See the
[action's README](https://github.com/EarthBuild/earthbuild/tree/main/.github/actions/failure-diagnostics)
for the inputs, including `CONTAINERS` if you build with a custom
installation name.

If it does turn out to be memory, GitHub's runners can be given more headroom
with a swapfile - EarthBuild's own CI uses
[`scripts/ci/add-swap.sh`](https://github.com/EarthBuild/earthbuild/blob/main/scripts/ci/add-swap.sh),
which places one on the runner's ephemeral disk rather than the root disk your
build layers need.

{% hint style='danger' %}

## actions/checkout ref argument

The example deliberately does not use the [`ref`](https://github.com/actions/checkout#checkout-a-different-branch) `actions/checkout@v4` option,
as it can lead to inconsistent builds where a user chooses to re-run an older commit which is no longer at the head of the branch.
{% endhint %}
