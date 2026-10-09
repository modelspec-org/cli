component "C" {
  field "f" {
    type = "int"
  }
}

record "A" {
  key = ["id"]
  field "id" {
    type = "int"
  }
  field "c" {
    record = "C"
  }
}
