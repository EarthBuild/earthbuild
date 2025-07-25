Earthly has the ability to run builds both locally and remotely. In this section, we will explore how to use remote runners to perform builds on remote machines.

## Remote Runners

Earthly is able to use remote runners for performing builds on remote machines. When Earthly uses a remote runner, the inputs of the build are picked up from the local environment, then the execution takes place remotely, including any pushes (`RUN --push` commands, and `SAVE IMAGE --push` commands), but any local outputs are sent back to the local environment. All this takes place while your local Earthly process still provides the logs of the build in real time locally.

Remote runners are especially useful in a few specific circumstances:

* You want to **reuse cache between CI runs** to dramatically speed up builds (more on this in part 8).
* You want to **share compute and cache with coworkers** and/or with the CI.
* You have **a build that requires a lot of resources**, and you want to run it on a machine with more resources than your local machine.
* You have **a build that requires running on a specific CPU architecture** natively.
* You have **a slow internet connection**.

There is one type of remote runner:

* Remote BuildKit (free, self-hosted)

### Using a Remote BuildKit

To run your own remote BuildKit, you can follow the instructions on the [remote BuildKit page](../ci-integration/remote-buildkit.md).

### Secrets and remote builds

When running remote builds, some operations might require access to secrets. For example, if you are pushing images to a private registry, or if you are logged in to DockerHub to prevent rate limiting. Earthly will automatically pass the credentials from your local machine to the remote runner.

Any secret that is available locally, including Docker/Podman credentials, will be passed to the remote runner whenever needed by the build.

For more information about secrets, see the [Secrets guide](../guides/secrets.md) and the [authenticating Git and image registries page](../guides/auth.md).
