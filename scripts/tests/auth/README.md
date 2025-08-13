These tests are for tests that cannot run via earthbuild-in-earthbuild.

If you add or remote any tests from this directory, the corresponding entry in `.github/workflows/ci.yml`
must also be updated by running:

    earthbuild +generate-github-tasks
