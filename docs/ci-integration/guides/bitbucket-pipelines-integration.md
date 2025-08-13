# Bitbucket Pipelines integration

Bitbucket Pipelines run in a shared Docker environment and do not support running earthbuild builds directly due to [restrictions](https://jira.atlassian.com/browse/BCLOUD-21419) that Bitbucket has put in place.

You can however, run EarthBuild builds on Bitbucket pipelines using the official EarthBuild Docker image. Here is an example of a Bitbucket Pipeline build. This example assumes your Earthfile has a `+build` target defined.

```yml
# ./bitbucket-pipelines.yml

image: earthbuild/earthbuild:v0.8.16

pipelines:
  default:
    - step:
        name: "Set earthbuild token"
        script:
          - export EARTHBUILD_TOKEN=$EARTHBUILD_TOKEN
    - step:
        name: "Docker login"
        script:
          - docker login --username "$DOCKERHUB_USERNAME" --password "$DOCKERHUB_TOKEN"
    - step:
        name: "Build"
        script:
          - earthbuild --ci --push --sat $EARTHBUILD_SAT --org $EARTHBUILD_ORG +build
```

For a complete guide on CI integration see the [CI integration guide](../overview.md).
