# Bitbucket Pipelines integration

Bitbucket Pipelines run in a shared Docker environment and do not support running EarthBuild builds directly due to [restrictions](https://jira.atlassian.com/browse/BCLOUD-21419) that Bitbucket has put in place.

You can however, run EarthBuild builds on Bitbucket pipelines via [remote buildkit](../../ci-integration/remote-buildkit.md). Because Bitbucket Pipelines run as containers you can also use the official EarthBuild Docker image.
