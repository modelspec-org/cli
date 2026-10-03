entity "Price" {
  key = ["code"]
  property "code" {
    type = "string"
    pattern = <<EOT
^[a-z]+$
EOT
  }
}
