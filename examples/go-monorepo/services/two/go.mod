module github.com/EarthBuild/earthbuild/examples/go-monorepo/services/two

go 1.25.0

require (
	github.com/EarthBuild/earthbuild/examples/go-monorepo/libs/hello v0.0.0
	github.com/labstack/echo/v5 v5.1.1
)

replace github.com/EarthBuild/earthbuild/examples/go-monorepo/libs/hello v0.0.0 => ../../libs/hello

require golang.org/x/net v0.53.0 // indirect
