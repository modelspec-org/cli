entity "Price" {
  key = ["code"]
  property "code" {
    type = "string"
    pattern = "^\$[0-9]+\%$"
  }
}
