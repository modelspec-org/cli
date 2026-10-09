record "Price" {
  key = ["code"]
  field "code" {
    type = "string"
    pattern = "^\$[0-9]+\%$"
  }
}
