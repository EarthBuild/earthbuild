import org.scalatest.flatspec.AnyFlatSpec

class DataVersionSpec extends AnyFlatSpec {

  val dv = new DataVersion()
  "Data Version " should " be positive" in {
    assert(dv.version() > 0)
  }
}