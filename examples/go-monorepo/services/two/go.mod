module github.com/EarthBuild/earthbuild/examples/go-monorepo/services/two

go 1.25.0

require (
	github.com/EarthBuild/earthbuild/examples/go-monorepo/libs/hello v0.0.0
	github.com/labstack/echo/v4 v4.15.1
	github.com/labstack/echo/v5 v5.0.4
)

replace github.com/EarthBuild/earthbuild/examples/go-monorepo/libs/hello v0.0.0 => ../../libs/hello

require (
	github.com/labstack/gommon v0.4.2 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasttemplate v1.2.2 // indirect
	golang.org/x/crypto v0.47.0 // indirect
	golang.org/x/net v0.49.0 // indirect
	golang.org/x/sys v0.40.0 // indirect
	golang.org/x/text v0.33.0 // indirect
)
