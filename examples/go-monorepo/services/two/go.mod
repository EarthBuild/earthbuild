module github.com/EarthBuild/earthbuild/examples/go-monorepo/services/two

go 1.26

require (
	github.com/EarthBuild/earthbuild/examples/go-monorepo/libs/hello v0.0.0
	github.com/labstack/echo/v5 v5.2.1
)

replace github.com/EarthBuild/earthbuild/examples/go-monorepo/libs/hello v0.0.0 => ../../libs/hello

require golang.org/x/net v0.56.0 // indirect
