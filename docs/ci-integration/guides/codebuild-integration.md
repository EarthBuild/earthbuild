
# AWS CodeBuild integration

Here is an example of an AWS CodeBuild build, where we build the earthbuild target `+build`.

{% hint style='info' %}
##### Note

Ensure when you're creating your CodeBuild Project that you enable the `Privileged` flag
in order to allow earthbuild build Docker images.

{% endhint %}

```yml
# ./buildspec.yml
version: 0.2

phases:
  install:
    commands:
      - wget https://github.com/earthbuild/earthbuild/releases/download/v0.8.16/earthbuild-linux-amd64 -O /usr/local/bin/earthbuild && chmod +x /usr/local/bin/earthbuild
  pre_build:
    commands:
      - echo Logging into Docker
      - docker login --username "$DOCKERHUB_USERNAME" --password "$DOCKERHUB_TOKEN"
  build:
    commands:
      - earthbuild --ci --push +build
```

For a complete guide on CI integration see the [CI integration guide](../overview.md).
