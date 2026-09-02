# Alternative Installation

This page outlines alternative installation instructions for the `earth` build tool. The main instructions that most users need are available on the [installation page](https://www.earthbuild.dev/install.html).

## Prerequisites

- [Docker](https://docs.docker.com/install/), [Podman](https://docs.podman.io/en/latest/), or [Apple Container](https://github.com/apple/container) (macOS)
- [Git](https://git-scm.com/book/en/v2/Getting-Started-Installing-Git)
- (*Windows only*) [Docker WSL 2 backend](https://docs.docker.com/docker-for-windows/wsl/) or [Podman WSL2 backend](https://github.com/containers/podman/blob/main/docs/tutorials/podman-for-windows.md)

## Install earth

Download the binary relevant to your platform from [the releases page](https://github.com/EarthBuild/earthbuild/releases), rename it to `earth` and place it in your `bin`.

To initialize the installation, including adding auto-completion for your shell, run

```bash
sudo earth bootstrap --with-autocomplete
```

and then restart your shell.

### CI

For instructions on how to install `earth` for CI use, see the [CI integration guide](../ci-integration/overview.md).

### Checksum Verification

You may optionally verify the checksum of the downloaded binaries, by performing the following steps:

1. Download our public key:

    ```bash
    wget -O earthbuild.pgp https://raw.githubusercontent.com/EarthBuild/earthbuild/main/release/apt-repo/earthbuild-pgp-public.pgp
    ```

2. Verify the public key was correctly downloaded:

    ```bash
    md5sum earthbuild.pgp
    ```

    which should produce:

    ```
    ddd123d902fb61a2310e7e506abd55f3  earthbuild.pgp
    ```

3. Import our key:

    ```bash
    gpg --import earthbuild.pgp
    ```

4. Trust our key:

    ```bash
    echo -e "5\ny\n" |  gpg --command-fd 0 --expert --edit-key 08900479B981AF7C32C8B918604C8879FF83C260 trust
    ```

5. Download the released `checksum.asc` file:

    You can manually download it from the [the releases page](https://github.com/EarthBuild/earthbuild/releases).

    The latest version can be fetched from the command line with:

    ```bash
    wget https://github.com/earthbuild/earthbuild/releases/latest/download/checksum.asc
    ```

6. Verify the `checksum.asc` file was released correctly:

    ```bash
    gpg --verify checksum.asc && gpg --verify --output checksum checksum.asc
    ```

{% hint style='danger' %}

#### gpg is dangerous

Don't be tempted to remove the initial `gpg --verify checksum.asc` command; gpg will still output the `checksum` file even
if the signature verification fails.
{% endhint %}

7. Verify the earth binary checksum matches

    ```bash
    sha256sum --check checksum --ignore-missing
    ```

    This should display an entry similar to:

    ```
    earth-linux-amd64: OK
    ```

### Installing from deb/rpm repositories

{% hint style='danger' %}

##### Currently unavailable

The deb and rpm repositories are **not currently available**.

These repositories were hosted at `pkg.earthly.dev`, which was decommissioned along with the rest of
the Earthly infrastructure; that hostname no longer resolves. EarthBuild has not yet stood up
replacement package repositories.

Until replacements are published, install the binary directly (see [Install earth](#install-earth)
above) or build [from source](#installing-from-source).

{% endhint %}

All EarthBuild release binaries are signed with our
[PGP key](https://raw.githubusercontent.com/EarthBuild/earthbuild/main/release/apt-repo/earthbuild-pgp-public.pgp),
which has the fingerprint:

    0890 0479 B981 AF7C 32C8 B918 604C 8879 FF83 C260

Note that this is a *different* key from the one Earthly used to sign its releases
(`5816 B221 3DD1 CEB6 1FC9 52BA B118 5ECA 33F8 EB64`). If you verified Earthly releases previously,
you will need to import the new key — see [Checksum Verification](#checksum-verification) above.

### Native Windows

{% hint style='danger' %}

##### Important

Our native Windows release is currently in the **Experimental** stage.

- The release ships with known issues. Many things work, but some don't.
- Check the [GitHub tracking issue](https://github.com/earthly/earthly/issues/1031) for any known problems.

{% endhint %}

To install the Windows release, simply [download](https://github.com/EarthBuild/earthbuild/releases/latest/download/earth-windows-amd64.exe) the binary (or from our [release page](https://github.com/EarthBuild/earthbuild/releases/latest/)); and ensure it is within your `PATH`.

To add `earth.exe` to your `PATH` environment variable:

1. Search and select: System (Control Panel)
2. Click the Advanced system settings link.
3. Click Environment Variables. In the "System Variables" section, select the PATH environment variable and click Edit.
   - If the PATH environment variable does not exist, click New.
4. In the Edit window, specify the value of the PATH environment variable, and Click OK.
5. Close and reopen any existing terminal windows, so they will pick up the new `PATH`.

If you are going to mostly be working from a WSL2 prompt in Windows, you might want to consider following the Linux instructions for installation. This will help prevent any cross-subsystem file transfers and keep your builds fast. Note that the "original" WSL is unsupported.

### macOS Binary

While installing `earth` via Homebrew is the recommended approach, you can also download a binary directly. This may be useful when using `earth` on a Mac in CI scenarios.

- [Apple silicon (arm64) binary](https://github.com/EarthBuild/earthbuild/releases/latest/download/earth-darwin-arm64)
- [Intel (x64) binary](https://github.com/EarthBuild/earthbuild/releases/latest/download/earth-darwin-amd64)

When using a precompiled binary, you may need to add an exception to Gatekeeper. [Follow Apple's instructions to add this exception](https://support.apple.com/guide/mac-help/apple-cant-check-app-for-malicious-software-mchleab3a043/mac).

### Installing from source

To install from source, see the [contributing page](https://github.com/earthbuild/earthbuild/blob/main/CONTRIBUTING.md).

## Configuration

If you use SSH-based git authentication, then your git credentials will just work with EarthBuild. Read more about [git auth](../guides/auth.md).

For a full list of configuration options, see the [Configuration reference](../earthly-config/earthly-config.md)

## Verify installation

To verify that the installation works correctly, you can issue a simple build of an existing hello-world project

```bash
earth github.com/EarthBuild/hello-world:main+hello
```

You should see the output

```
github.com/EarthBuild/hello-world:main+hello | --> RUN [echo 'Hello, world!']
github.com/EarthBuild/hello-world:main+hello | Hello, world!
github.com/EarthBuild/hello-world:main+hello | Target github.com/EarthBuild/hello-world:main+hello built successfully
=========================== SUCCESS ===========================
```

# Uninstall

To remove earth, run the following commands:

## macOS users

```bash
brew uninstall earthbuild/tap/earth
rm -rf ~/.earth

# Docker:
docker rm --force earth-buildkitd 2>/dev/null || true
docker volume rm --force earth-cache 2>/dev/null || true

# Podman:
podman rm --force earth-buildkitd 2>/dev/null || true
podman volume rm --force earth-cache 2>/dev/null || true

# Apple Container:
container delete -f earth-buildkitd 2>/dev/null || true
container volume delete -f earth-cache 2>/dev/null || true
```

## Linux and WSL2 users

```bash
rm /usr/local/bin/earth
rm /usr/share/bash-completion/completions/earth
rm /usr/local/share/zsh/site-functions/_earth
rm -rf ~/.earth
docker rm --force earth-buildkitd
docker volume rm --force earth-cache
```

If you previously had Earthly installed, its state lives under the old names and is not removed by
the commands above. To clean that up as well:

```bash
rm -rf ~/.earthly
docker rm --force earthly-buildkitd
docker volume rm --force earthly-cache
```
