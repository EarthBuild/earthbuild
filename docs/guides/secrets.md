# Secrets

Secrets are sensitive data that should not be stored in the Earthfile. For example, a password or an API key. Secrets are similar to build arguments, but they are not stored in the Earthfile and are not cached.

Access to secrets is declared explicitly in the commands that need them.

## Passing secrets to RUN commands

Here's an example Earthfile that accesses a secret stored under `passwd` and exposes it under the environment variable `mypassword`:

```dockerfile
FROM alpine:latest
hush:
    RUN --secret mypassword=passwd echo "my password is $mypassword"
```

If the environment variable name is identical to the secret ID. For example to accesses a secret stored under `passwd` and exposes it under the environment variable `passwd`  you can use the shorthand :

```dockerfile
FROM alpine:latest
hush:
    RUN --secret passwd echo "my password is $passwd"
```

{% hint style='info' %}
It's also possible to temporarily mount a secret as a file:

```dockerfile
RUN --mount type=secret,target=/root/mypassword,id=passwd echo "my password is $(cat /root/mypassword)"
```

The file will not be saved to the image snapshot.
{% endhint %}

## Setting secret values

The value for `passwd` in examples above must then be supplied when earthly is invoked.

This is possible in a few ways:


1. Directly, on the command line:

   ```bash
   earthly --secret passwd=itsasecret +hush
   ```

2. Via an environment variable:

   ```bash
   export passwd=itsasecret
   earthly --secret passwd +hush
   ```

   If the value of the secret is omitted on the command line Earthly will lookup the environment variable with that name.

3. Via the environment variable `EARTHLY_SECRETS`

   ```bash
   export EARTHLY_SECRETS="passwd=itsasecret"
   earthly +hush
   ```

   Multiple secrets can be specified by separating them with a comma.

4. Via the `.secret` file.

   Create a `.secret` file in the same directory where you plan to run `earthly` from. Its contents should be:
   
   ```
   passwd=itsasecret
   ```
   
   Then simply run earthly:
   
   ```bash
   earthly +hello
   ```

5. Via your own secret manager. To share secrets across a team, fetch them from a system you
   control (for example HashiCorp Vault, AWS Secrets Manager, or your CI provider's secret store)
   and pass them in using one of the approaches above.

Regardless of the approach chosen from above, once earthly is invoked, in our example, it will output:

```
+hush | --> RUN echo "my password is $mypassword"
+hush | my password is itsasecret
```

{% hint style='info' %}
### How Arguments and Secrets affect caching

Commands in earthly must be re-evaluated when the command itself changes (e.g. `echo "hello $name"` is changed to `echo "greetings $name"`), or when
one of its inputs has changed (e.g. `--name=world` is changed to `--name=banana`). Earthly creates a hash based on both the contents
of the command and the contents of all defined arguments of the target build context.

However, in the case of secrets, the contents of the secret *is not* included in the hash; therefore, if the contents of a secret changes, Earthly is unable to
detect such a change, and thus the command will not be re-evaluated.
{% endhint %}

## Storage of local secrets

Earthly stores the contents of command-line-supplied secrets in memory on the localhost. When a `RUN` command that requires a secret is evaluated by BuildKit, the BuildKit
daemon will request the secret from the earthly command-line process and will temporarily mount the secret inside the runc container that is evaluating the `RUN` command.
Once the command finishes the secret is unmounted. It will not persist as an environment variable within the saved container snapshot. Secrets will be kept in-memory
until the earthly command exits.

Secrets are never written to disk by EarthBuild, and never leave the machine running the build.

{% hint style='info' %}

##### Cloud-based secrets have been removed

Earthly offered a hosted secret store, managed with `earthly secret set ...`. That service and the
`secret`/`secrets` commands were removed as part of the cloud teardown and are not part of
EarthBuild. To share secrets across a team, fetch them from a secret manager you control and pass
them in via `--secret`, `--secret-file-path`, or the environment. See
[Migrating from earthly](../migrating-from-earthly.md) for the full list of removed commands.

{% endhint %}
