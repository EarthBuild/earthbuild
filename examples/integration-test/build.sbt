lazy val scalatest = "org.scalatest" %% "scalatest" % "3.2.19"

scalaVersion := "3.8.2"
name := "scala-example"
organization := "earthly.dev"
version := "1.0"

libraryDependencies ++= Seq(
  "org.tpolecat" %% "doobie-core"      % "1.0.0-RC2",
  "org.tpolecat" %% "doobie-postgres"  % "1.0.0-RC12",         
  "org.tpolecat" %% "doobie-scalatest" % "1.0.0-RC2" % "test" 
)

lazy val root = (project in file("."))
  .configs(IntegrationTest)
  .settings(
    Defaults.itSettings,
    libraryDependencies += scalatest % "it,test"
  )