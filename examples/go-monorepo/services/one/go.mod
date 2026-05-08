module github.com/EarthBuild/earthbuild/examples/go-monorepo/services/one

go 1.25.0

require (
	github.com/EarthBuild/earthbuild/examples/go-monorepo/libs/hello v0.0.0
	github.com/labstack/echo/v5 v5.1.1
	github.com/labstack/echo/v5 v5.1.1
)

replace github.com/EarthBuild/earthbuild/examples/go-monorepo/libs/hello v0.0.0 => ../../libs/hello

require (
	github.com/labstack/gommon v0.5.0 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.22 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasttemplate v1.2.2 // indirect
	golang.org/x/crypto v0.50.0 // indirect
	golang.org/x/net v0.53.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
	golang.org/x/text v0.36.0 // indirect
)
