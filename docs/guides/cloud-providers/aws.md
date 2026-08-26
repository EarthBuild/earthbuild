# Accessing AWS resources

## Introduction

It is common for builds to be able to access AWS resources (For example, one might want to upload artifacts to S3).
EarthBuild provides two ways to easily authenticate to AWS in order to access resources.

## Authentication Methods

### Local Environment Credentials

EarthBuild is able to access AWS credentials from the host.
The credentials might be available via environment variables or your `~/.aws` directory.

To use these credentials simply use `RUN --aws in your command`.

For example:
```dockerfile
VERSION --run-with-aws 0.8

aws:
    FROM amazon/aws-cli
    RUN --aws aws s3 ls
```

For more information, see [here](../../earthfile/earthfile.md#--aws-experimental).

### OIDC (OpenID Connect)

OIDC in useful in cases where credentials are not available in the host (e.g. CI system)
and/or when authentication requires MFA (multi-factor authentication).

{% hint style='danger' %}

##### The hosted OIDC provider is unavailable

Setting this up required registering EarthBuild's hosted OIDC issuer (`api.earthly.dev`) as an
identity provider in AWS IAM. That host was decommissioned along with EarthBuild Cloud and no longer
resolves. Use `RUN --aws` with credentials supplied by your CI provider instead.

Progress is tracked in [issue #750](https://github.com/EarthBuild/earthbuild/issues/750).

{% endhint %}
