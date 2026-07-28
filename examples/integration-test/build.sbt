lazy val scalatest = "org.scalatest" %% "scalatest" % "3.2.20"

scalaVersion := "3.8.4"
name := "scala-example"
organization := "earthly.dev"
version := "1.0"

libraryDependencies ++= Seq(
  "org.tpolecat" %% "doobie-core"      % "1.0.0-RC12",
  "org.tpolecat" %% "doobie-postgres"  % "1.0.0-RC12",
  "org.tpolecat" %% "doobie-scalatest" % "1.0.0-RC12" % "test"
)

lazy val IntegrationTest = config("it") extend(Test)

lazy val root = (project in file("."))
  .configs(IntegrationTest)
  .settings(
    inConfig(IntegrationTest)(Defaults.testSettings),
    IntegrationTest / scalaSource := baseDirectory.value / "src" / "it" / "scala",
    IntegrationTest / resourceDirectory := baseDirectory.value / "src" / "it" / "resources",
    libraryDependencies += scalatest % "it,test",
    assembly / assemblyOutputPath := Def.uncached { file("target/assembly/scala-example-assembly-1.0.jar") }
  )