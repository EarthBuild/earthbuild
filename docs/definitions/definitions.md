# Definitions

This page presents some common terms used throughout the earthbuild documentation. Understanding these terms with help you understand how to use earthbuild. 

* **earthbuild** - the build automation system as a whole
* **`earthbuild`** - the CLI tool used to interact with earthbuild
* **Earthfile** - a file (named literally `Earthfile`) which contains a series of targets and their respective recipes
* **buildkitd** - a [daemon built by the Docker team](https://github.com/moby/buildkit) and used by EarthBuild to execute builds. It executes LLB, the same low-level primitives used when building Dockerfiles. The buildkitd daemon is started automatically in a docker container, by `earthbuild`, when executing builds.
* **recipe** - a specific series of build steps
* **target** - the label used to identify a recipe. 'Target' is also used to refer to a build of a specific target.
* **build context** - the main directory made available to the build for copying files from
* **artifact** - a file resulting from executing a target (not all targets have artifacts)
* **image** - a docker image resulting from executing a target (not all targets have images)

## See also

* The [Earthfile reference](../earthfile/earthfile.md)
* The [earthbuild command reference](../earthbuild-command/earthbuild-command.md)
