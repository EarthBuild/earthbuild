# earthbuild Debian repository

We host a Debian repository which Debian and ubuntu users can use to install earthbuild.

## Setup for Ubuntu

TODO: move these notes elsewhere, this readme should only be notes on how to release to our repo, and is only intended for those with
access to earthbuild credentials.

Ubuntu users can use this guide to set up our repo:

First install the following tools:

    sudo apt-get update
    sudo apt-get install \
       apt-transport-https \
       ca-certificates \
       curl \
       gnupg \
       lsb-release

Second, add earthbuild's official GPG key:

    curl -fsSL https://pkg.earthbuild.dev/earthbuild.pgp | sudo gpg --dearmor -o /usr/share/keyrings/earthbuild-archive-keyring.gpg


Finally, set up the stable repository:

    echo \
      "deb [arch=amd64 signed-by=/usr/share/keyrings/earthbuild-archive-keyring.gpg] https://pkg.earthbuild.dev/deb \
      stable main" | sudo tee /etc/apt/sources.list.d/earthbuild.list > /dev/null

## Requirements

To package a new version of earthbuild, ensure the following requirements are met:

1. you have aws credentials configured in the earthbuild secret store under `/user/earthbuild-technologies/aws/credentials`, and have access to the developer role

    # you can upload them via
    earthbuild secrets set --file ~/.aws/credentials /user/earthbuild-technologies/aws/credentials

2. you have access to the earthbuild-technologies secrets; specifically the following two commands should work:

    earthbuild secrets ls /earthbuild-technologies/apt/keys/earthbuild-apt-public.pgp
    earthbuild secrets ls /earthbuild-technologies/apt/keys/earthbuild-apt-private.pgp

## Release steps

Once earthbuild has been released to GitHub, visit https://github.com/earthbuild/earthbuild/releases to determine the latest version:

    export RELEASE_TAG="v0.0.0"

Then run

    earthbuild +build-and-release --RELEASE_TAG="$RELEASE_TAG"

### Running steps independently

It is also possible to run steps independently:

#### Building deb packages

To package all platforms

    earthbuild +deb-all --RELEASE_TAG="$RELEASE_TAG"

To package a specific platform

    earthbuild +deb --RELEASE_TAG="$RELEASE_TAG" --EARTHBUILD_PLATFORM=arm7

#### Cloning the s3 repo to your local disk

    earthbuild +download

#### Indexing and signing the repo

    earthbuild +index-and-sign

#### Uploading the repo to s3

    earthbuild +upload
