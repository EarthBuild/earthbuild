# Pushing and Pulling images with Azure ACR

## Introduction

The Azure Container Registry (ACR) is a hosted docker repository that requires extra configuration for day-to-day use. This configuration is not typical of other repositories, and there are some considerations to account for when using it with earthbuild. This guide will walk you through creating an Earthfile, building an image, and pushing it to ACR.


This guide assumes you have already installed the [Azure CLI tool](https://docs.microsoft.com/en-us/cli/azure/install-azure-cli), and [created a new repository named `helloearthbuild`](https://portal.azure.com/?quickstart=true#create/Microsoft.ContainerRegistry).

## Create an Earthfile

No special considerations are needed in the Earthfile itself. You can use `SAVE IMAGE` just like any other repository.

```
FROM alpine:3.18

build:
    RUN echo "Hello from earthbuild!" > motd
    ENTRYPOINT cat motd
    SAVE IMAGE --push helloearthbuild.azurecr.io/hello-earthbuild:with-love
```

## Login and Configure the ACR Credential Helper

ACR does not issue permanent credentials. Instead, it relies on your Azure AD credentials to issue Docker credentials. As an individual user, you will need to log into your repository first:

```
❯ az acr login --name helloearthbuild
Login Succeeded
```

After logging in, the [ACR Credential Helper](https://github.com/Azure/acr-docker-credential-helper) will help keep your credentials up to date, as long as it is invoked again before your already issued credentials expire. When all this is complete, your `.docker/config.json` might look like this:
```
{
	"auths": {
		"helloearthbuild.azurecr.io": {
			"auth": "...",
			"identitytoken": "..."
		}
	},
	"credsStore": "acr-linux"
}
```

ACR boasts many other methods of logging in, including [Service Principals](https://docs.microsoft.com/en-us/azure/container-registry/container-registry-auth-service-principal) and [admin accounts](https://docs.microsoft.com/en-us/azure/container-registry/container-registry-authentication#admin-account). Note that the admin account method is not recommended for production usage. Please follow the relevant guides to authenticate if you wish to use one of these other methods.

## RBAC

Ensure that you have correct permissions to push and pull the images. Please reference the [ACR RBAC documentation](https://docs.microsoft.com/en-us/azure/container-registry/container-registry-roles) to ensure you have the correct permissions set. To complete all the activities in this guide, you will need to have at least the `AcrPush` role.

earthbuild also works with Service Principals; and these do not require `az acr login`. You can simply login directly with `docker` like this: 

```
RUN --secret AZ_USERNAME=earthbuild-technologies/azure/ci-cd-username \
    --secret AZ_PASSWORD=earthbuild-technologies/azure/ci-cd-password \
    docker login helloearthbuild.azurecr.io --username $AZ_USERNAME --password $AZ_PASSWORD
```

## Run the Target

Once you are logged in, and have the optional credential helper installed, then you are ready to use earthbuild to access images in ACR. To build and push an image, simply execute the build target. Don't forget the `--push` flag!

```
❯ ../earthbuild/earthbuild --push --no-cache +build
           buildkitd | Found buildkit daemon as docker container (earthbuild-buildkitd)
         alpine:3.18 | --> Load metadata linux/amd64
               +base | --> FROM alpine:3.18
               +base | [██████████] resolve docker.io/library/alpine:3.18@sha256:0bd0e9e03a022c3b0226667621da84fc9bf562a9056130424b5bfbd8bcb0397f ... 100%
              +build | --> RUN echo "Hello from earthbuild!" > motd
              output | --> exporting outputs
              output | [██████████] exporting layers ... 100%
              output | [██████████] exporting manifest sha256:02df2d4600094d5550f7475b868ce9bb17d6c3a529e9669a453bbba7b2cdb659 ... 100%
              output | [██████████] exporting config sha256:722368416f5de51291ce937feac2c246d66dff351678968b1b6ebc533ceaaa0c ... 100%
              output | [██████████] pushing layers ... 100%
              output | [██████████] pushing manifest for helloearthbuild.azurecr.io/hello-earthbuild:with-love ... 100%
              output | [██████████] sending tarballs ... 100%
824d26cf8432: Loading layer [==================================================>]     192B/192B
=========================== SUCCESS ===========================
Loaded image: helloearthbuild.azurecr.io/hello-earthbuild:with-love
              +build | Image +build as helloearthbuild.azurecr.io/hello-earthbuild:with-love (pushed)
```

## Pulling Images

By logging in and optionally installing the credential helper; you can also pull images without any special handling in an Earthfile:

```
FROM earthbuild/dind:alpine-main

run:
    WITH DOCKER --pull helloearthbuild.azurecr.io/hello-earthbuild:with-love
        RUN docker run helloearthbuild.azurecr.io/hello-earthbuild:with-love
    END
```

And here is how you would run it:

```
❯ earthbuild -P +run
           buildkitd | Found buildkit daemon as docker container (earthbuild-buildkitd)
  e/dind:alpine-main | --> Load metadata linux/amd64
h/hello-earthbuild:with-love | --> Load metadata linux/amd64
h/hello-earthbuild:with-love | --> DOCKER PULL helloearthbuild.azurecr.io/hello-earthbuild:with-love
h/hello-earthbuild:with-love | [██████████] resolve helloearthbuild.azurecr.io/hello-earthbuild:with-love@sha256:02df2d4600094d5550f7475b868ce9bb17d6c3a529e9669a453bbba7b2cdb659 ... 100%
               +base | --> FROM earthbuild/dind:alpine-main
               +base | [██████████] resolve docker.io/earthbuild/dind:alpine-main@sha256:09f497f0114de1f3ac6ce2da05568fcb50b0a4fd8b9025ed7c67dc952d092766 ... 100%
                +run | *cached* --> WITH DOCKER (install deps)
                +run | --> WITH DOCKER RUN docker run helloearthbuild.azurecr.io/hello-earthbuild:with-love
                +run | Loading images...
                +run | Loaded image: helloearthbuild.azurecr.io/hello-earthbuild:with-love
                +run | ...done
                +run | Hello from earthbuild!
              output | --> exporting outputs
              output | [██████████] sending tarballs ... 100%
=========================== SUCCESS ===========================
```

## Troubleshooting

### 401 (authentication required)

Re-run `az acr login --name` to log in again and refresh your credentials. Azure recommends that you run this at the beginning o each automated script; keep this in mind for your CI runs.
