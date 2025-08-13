
# Circle CI integration

Here is an example of a Circle CI build, where we build the earthbuild target `+build`.

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
      - run: "sudo /bin/sh -c 'wget https://github.com/earthbuild/earthbuild/releases/download/v0.8.16/earthbuild-linux-amd64 -O /usr/local/bin/earthbuild && chmod +x /usr/local/bin/earthbuild'"
      - run: earthbuild --ci --push +build
```

For a complete guide on CI integration see the [CI integration guide](../overview.md).
