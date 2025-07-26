# Guide for setting up CI on an Earthly Fork

This document outlines the steps and considerations for setting up a functional GitHub Actions CI on a fork of the `earthly/earthly` repository, under the new organization `earthbuild`.

## 1. Repository Dependencies

The CI workflows depend on several external GitHub repositories and actions. These will need to be forked and updated within the workflow files.

- **`earthly/actions-setup`**: This action is used in multiple workflows to set up the `earthly` environment.
  - **Action Required**: Fork `github.com/earthly/actions-setup` to `github.com/earthbuild/actions-setup`. Review the forked action for any hardcoded references to the `earthly` organization and update them.
  - **Update Workflows**: Change all occurrences of `uses: earthly/actions-setup@main` to `uses: earthbuild/actions-setup@main` in all workflow files.

- **`earthly/earthly-staging`**: The `ci-staging-deploy.yml` workflow pushes to `git@github.com:earthly/earthly-staging.git`.
  - **Action Required**: Create a new repository `github.com/earthbuild/earthly-staging`.
  - **Update Workflows**: The `release/release.sh` script, called by the staging workflow, needs to be updated to push to this new repository.

- **`earthly/homebrew-earthly`**: The release script interacts with a Homebrew tap repository.
  - **Action Required**: Create a new repository `github.com/earthbuild/homebrew-earthly`.
  - **Update Workflows**: The `release/release.sh` script needs to be updated to use this new tap.

- **`earthly/buildkit`**: The release script uses a specific commit from an `earthly-next` file, which points to a commit in a fork of buildkit.
  - **Action Required**: The community will need to decide how to manage the buildkit dependency. The simplest approach is to use an official buildkit release. If custom patches are needed, a fork `github.com/earthbuild/buildkit` will be necessary.

## 2. Secrets Management

The CI workflows require numerous secrets. These need to be created in the `earthbuild/earthly` repository settings (`Settings -> Secrets and variables -> Actions`).

- **`EARTHLY_TOKEN`**: For interacting with Earthly Cloud (e.g., for remote caching).
  - **Action Required**: Create an account on [Earthly Cloud](https://cloud.earthly.dev) for the `earthbuild` organization and generate a token.

- **`DOCKERHUB_USERNAME`** and **`DOCKERHUB_TOKEN`**: For pushing and pulling images from Docker Hub.
  - **Action Required**: Create a Docker Hub organization/account for `earthbuild` and create an access token.

- **`GITHUB_TOKEN`**: This is generally provided by GitHub Actions, but it is also used explicitly in some scripts. Permissions for the token might need to be adjusted.

- **Deploy Keys**: The release process uses an SSH key (`littleredcorvette-id_rsa`) to push to the staging and homebrew repositories.
  - **Action Required**: Generate a new SSH key pair. Add the public key as a deploy key with write access to `earthbuild/earthly-staging` and `earthbuild/homebrew-earthly`. Add the private key as a secret (e.g., `DEPLOY_KEY`) in the `earthbuild/earthly` repository. The `release/release.sh` script will need to be updated to use this new secret.

## 3. Service Dependencies

The CI relies on several external services.

- **Private Docker Mirror (`registry-1.docker.io.mirror.corp.earthly.dev`)**: This was originally a major blocker. The private mirror has been replaced with Google's public mirror (`mirror.gcr.io`).
  - **Action Required**: Remove all steps and configurations related to this mirror. This includes:
    - `docker login` steps for the mirror.
    - `earthly config` commands that set the mirror.
    - The associated secrets `DOCKERHUB_MIRROR_USERNAME` and `DOCKERHUB_MIRROR_PASSWORD`.
    - These changes are needed in `build-earthly.yml`, `ci-docker-ubuntu.yml`, and the local action `.github/actions/stage2-setup/action.yml`.

- **Earthly Cloud Secret Management**: The CI uses `earthly secret --org earthly-technologies ...` to fetch secrets.
  - **Action Required**: This is a major blocker. The community fork will not have access to the `earthly-technologies` organization secrets. All calls to `earthly secret` must be reviewed. For deploy keys, the key should be stored as a GitHub secret. Other secrets need to be handled similarly.

- **AWS S3**: The release script `release/release.sh` uploads packages (APT, YUM) to an S3 bucket (`production-pkg` or `staging-pkg`).
  - **Action Required**: The community will need to set up its own SS3 bucket for package distribution and configure AWS credentials as secrets in the repository (`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_REGION`). The `release/release.sh` script will need to be updated to use these new variables.

## 4. CI/CD Blockers and Modifications

- **CLA/Contributor Agreement**: No explicit CLA check was found in the workflow files. However, it might be enforced by a GitHub App on the original repository. This should not be a blocker for the fork, as the check will not be present on the new repository unless explicitly configured.

- **Hardcoded Organization/Usernames**: The code, especially in the release scripts, contains hardcoded references to `earthly` as a user or organization.
  - **Action Required**: A thorough search for "earthly" should be performed, and all hardcoded values should be replaced with variables that can be configured for the `earthbuild` organization. The `release/release.sh` script is a good example of where this is handled with environment variables.

- **Local Action `.github/actions/stage2-setup`**: This composite action contains much of the problematic logic.
  - **Action Required**: This action needs to be heavily modified to remove the Docker mirror, the Earthly Cloud secret fetching, and other hardcoded values.

## Progress Update

- The private Docker mirror (`registry-1.docker.io.mirror.corp.earthly.dev`) has been replaced with Google's public mirror (`mirror.gcr.io`) in all workflows and scripts.
- The `.github/actions/stage2-setup/action.yml` has been updated to remove mirror-related inputs and steps.
- The main CI workflow, `ci-docker-ubuntu.yml`, has been simplified by removing jobs that depend on the private mirror, cloud provider secrets (GCP/ECR), and internal release processes.

## Next Steps

1.  **Fork and Update Actions**: Fork `earthly/actions-setup`.
2.  **Create Forked Repos**: Create `earthly-staging` and `homebrew-earthly` under `earthbuild`.
3.  **Create Secrets**: Set up all necessary secrets in the forked repository's settings.
4.  **Update Release Scripts**: Modify `release/release.sh` and other scripts to use the new `earthbuild` resources and secrets.
5.  **Review and Parameterize**: Go through workflows and scripts to replace hardcoded `earthly` references with configurable variables. 
