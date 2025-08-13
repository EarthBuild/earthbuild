# Alternative Installation

This page outlines alternative installation instructions for the `earthbuild` build tool. For standard installation instructions, see the [installation page](../install/install.md).

## Prerequisites

* [Docker](https://docs.docker.com/install/) or [Podman](https://docs.podman.io/en/latest/)
* [Git](https://git-scm.com/book/en/v2/Getting-Started-Installing-Git)
* (*Windows only*) [Docker WSL 2 backend](https://docs.docker.com/docker-for-windows/wsl/) or [Podman WSL2 backend](https://github.com/containers/podman/blob/main/docs/tutorials/podman-for-windows.md)

## Install earthbuild

Download the binary relevant to your platform from [the releases page](https://github.com/earthbuild/earthbuild/releases), rename it to `earthbuild` and place it in your `bin`.

To initialize the installation, including adding auto-completion for your shell, run

```bash
sudo earthbuild bootstrap --with-autocomplete
```

and then restart your shell.

### CI

For instructions on how to install `earthbuild` for CI use, see the [CI integration guide](../ci-integration/overview.md).

### Checksum Verification

You may optionally verify the checksum of the downloaded binaries, by performing the following steps:

1. Download our public key:

    ```bash
    wget https://pkg.earthbuild.dev/earthbuild.pgp
    ```

2. Verify the public key was correctly downloaded:

    ```bash
    md5sum earthbuild.pgp
    ```

    which should produce:

    ```
    8f455671610b15ee21be31e9f16b7bb6  earthbuild.pgp
    ```

3. Import our key:

    ```bash
    gpg --import earthbuild.pgp
    ```

4. Trust our key:

    ```bash
    echo -e "5\ny\n" |  gpg --command-fd 0 --expert --edit-key 5816B2213DD1CEB61FC952BAB1185ECA33F8EB64 trust
    ```

5. Download the released `checksum.asc` file:

    You can manually download it from the [the releases page](https://github.com/earthbuild/earthbuild/releases).

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

7. Verify the earthbuild binary checksum matches

    ```bash
    sha256sum --check checksum --ignore-missing
    ```

    This should display an entry similar to:

    ```
    earthbuild-linux-amd64: OK
    ```

### Installing from earthbuild repositories (**beta**)

{% hint style='danger' %}
##### Important

Our rpm and deb repositories are currently in **Beta** stage.

* Check the [GitHub tracking issue](https://github.com/earthbuild/earthbuild/issues) for any known problems.
* Give us feedback on [GitHub](https://github.com/earthbuild/earthbuild/issues).
{% endhint %}

EarthBuild can be installed for Debian and RedHat based Linux distributions. Note: The original earthbuild package repositories are no longer maintained. Manual installation is recommended.

Binary signatures may be available for official EarthBuild releases on GitHub.

    5816 B221 3DD1 CEB6 1FC9 52BA B118 5ECA 33F8 EB64

#### Debian-based repositories (including Ubuntu)

Debian-based Linux users (e.g. Debian, Ubuntu, Mint, etc) can use our apt repo to install EarthBuild.

Before installing earthbuild, you must first set up the earthbuild apt repo.

1. Update apt and install required tools to support https-based apt repos:

   ```bash
   sudo apt-get update
   sudo apt-get install \
      apt-transport-https \
      ca-certificates \
      curl \
      gnupg \
      lsb-release
   ```

2. Download earthbuild's GPG key:

   ```bash
   # Note: earthbuild.dev package repositories are no longer available
   # Use manual installation from GitHub releases instead
   ```

3. Setup the stable repo:

   ```bash
   # Note: earthbuild.dev package repositories are no longer available
   # Use manual installation from GitHub releases instead
   ```

4. Install earthbuild:

   ```bash
   sudo apt-get update
   sudo apt-get install earthbuild
   ```


#### Fedora repositories

Fedora users can use our rpm repo to install EarthBuild.

1. Install plugins required to manage DNF repositories:

   ```bash
   sudo dnf -y install dnf-plugins-core
   ```

2. Add the earthbuild repo to your system:

   ```bash
   # Note: earthbuild.dev package repositories are no longer available
   # Use manual installation from GitHub releases instead
   ```

3. Install earthbuild:

   ```bash
   sudo dnf install earthbuild
   ```

#### CentOS repositories

CentOS users can use our rpm repo to install EarthBuild.

1. Install utils required to manage yum repositories:

   ```bash
   sudo yum install -y yum-utils
   ```

2. Add the earthbuild repo to your system:

   ```bash
   # Note: earthbuild.dev package repositories are no longer available
   # Use manual installation from GitHub releases instead
   ```

3. Install earthbuild:

   ```bash
   sudo yum install earthbuild
   ```

### Native Windows

{% hint style='danger' %}
##### Important

Our native Windows release is currently in the **Experimental** stage.

* The release ships with known issues. Many things work, but some don't.
* Check the [GitHub issues](https://github.com/earthbuild/earthbuild/issues) for any known problems.
* Give us feedback on [GitHub](https://github.com/earthbuild/earthbuild/issues).

{% endhint %}

To install the Windows release, simply [download](https://github.com/earthbuild/earthbuild/releases/latest/download/earthbuild-windows-amd64.exe) the binary (or from our [release page](https://github.com/earthbuild/earthbuild/releases/latest/)); and ensure it is within your `PATH`.

To add `earthbuild.exe` to your `PATH` environment variable:

1. Search and select: System (Control Panel)
2. Click the Advanced system settings link.
3. Click Environment Variables. In the "System Variables" section, select the PATH environment variable and click Edit.
   * If the PATH environment variable does not exist, click New.
4. In the Edit window, specify the value of the PATH environment variable, and Click OK.
5. Close and reopen any existing terminal windows, so they will pick up the new `PATH`.

If you are going to mostly be working from a WSL2 prompt in Windows, you might want to consider following the Linux instructions for installation. This will help prevent any cross-subsystem file transfers and keep your builds fast. Note that the "original" WSL is unsupported.

### macOS Binary

While installing `earthbuild` via Homebrew is the recommended approach, you can also download a binary directly. This may be useful when using `earthbuild` on a Mac in CI scenarios.

* [M1 Binary](https://github.com/earthbuild/earthbuild/releases/latest/download/earthbuild-darwin-arm64)
* [x64 Binary](https://github.com/earthbuild/earthbuild/releases/latest/download/earthbuild-darwin-amd64)

When using a precompiled binary, you may need to add an exception to Gatekeeper. [Follow Apple's instructions to add this exception](https://support.apple.com/guide/mac-help/apple-cant-check-app-for-malicious-software-mchleab3a043/mac).

### Installing from source

To install from source, see the [contributing page](https://github.com/earthbuild/earthbuild/blob/main/CONTRIBUTING.md).

## Configuration

If you use SSH-based git authentication, then your git credentials will just work with earthbuild. Read more about [git auth](../guides/auth.md).

For a full list of configuration options, see the [Configuration reference](../earthbuild-config/earthbuild-config.md)

## Verify installation

To verify that the installation works correctly, you can issue a simple build of an existing hello-world project

```bash
earthbuild github.com/EarthBuild/hello-world:main+hello
```

You should see the output

```
github.com/EarthBuild/hello-world:main+hello | --> RUN [echo 'Hello, world!']
github.com/EarthBuild/hello-world:main+hello | Hello, world!
github.com/EarthBuild/hello-world:main+hello | Target github.com/EarthBuild/hello-world:main+hello built successfully
=========================== SUCCESS ===========================
```

# Uninstall

To remove earthbuild, run the following commands:

## macOS users

```bash
brew uninstall earthbuild
rm -rf ~/.earthbuild
docker rm --force earthbuild-buildkitd
docker volume rm --force earthbuild-cache
```

## Linux and WSL2 users

```bash
rm /usr/local/bin/earthbuild
rm /usr/share/bash-completion/completions/earthbuild
rm /usr/local/share/zsh/site-functions/_earthbuild
rm -rf ~/.earthbuild
docker rm --force earthbuild-buildkitd
docker volume rm --force earthbuild-cache
```
