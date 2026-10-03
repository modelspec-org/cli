component "C" {
  field "f" {
    type = "int"
  }
}

entity "A" {
  key = ["id"]
  property "id" {
    type = "int"
  }
  property "c" {
    entity = "C"
  }
}
