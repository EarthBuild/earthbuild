scalaVersion := "3.8.4"
name := "scala-example"
organization := "earthly.dev"
version := "1.0"

assembly / assemblyOutputPath := Def.uncached { file("target/assembly/scala-example-assembly-1.0.jar") }

libraryDependencies += "org.typelevel" %% "cats-core" % "2.13.0"