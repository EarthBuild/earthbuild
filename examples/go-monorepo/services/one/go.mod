module github.com/EarthBuild/earthbuild/examples/go-monorepo/services/one

go 1.26

require github.com/EarthBuild/earthbuild/examples/go-monorepo/libs/hello v0.0.0

replace github.com/EarthBuild/earthbuild/examples/go-monorepo/libs/hello v0.0.0 => ../../libs/hello
