
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
        version: v0.8.17
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
      run: "sudo /bin/sh -c 'wget https://github.com/EarthBuild/earthbuild/releases/download/v0.8.17/earth-linux-amd64 -O /usr/local/bin/earth && chmod +x /usr/local/bin/earth'"
    - name: Run build
      run: earth --ci --push +build
```

For a complete guide on CI integration see the [CI integration guide](../overview.md).

{% hint style='danger' %}
## actions/checkout ref argument

The example deliberately does not use the [`ref`](https://github.com/actions/checkout#checkout-a-different-branch) `actions/checkout@v4` option,
as it can lead to inconsistent builds where a user chooses to re-run an older commit which is no longer at the head of the branch.
{% endhint %}
