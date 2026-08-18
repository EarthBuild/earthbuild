
# Circle CI integration

Here is an example of a Circle CI build, where we build the EarthBuild target `+build`.

```yml
# .circleci/config.yml

version: 2.1
jobs:
  build:
    machine:
      image: ubuntu-2004:2023.02.1
    steps:
      - checkout
      - run: docker login --username "$DOCKERHUB_USERNAME" --password "$DOCKERHUB_TOKEN"
      - run: "sudo /bin/sh -c 'wget https://github.com/EarthBuild/earthbuild/releases/download/v0.8.18/earth-linux-amd64 -O /usr/local/bin/earth && chmod +x /usr/local/bin/earth'"
      - run: earth --ci --push +build
```

For a complete guide on CI integration see the [CI integration guide](../overview.md).
