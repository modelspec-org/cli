record "Price" {
  key = ["code"]
  field "code" {
    type = "string"
    pattern = <<EOT
^[a-z]+$
EOT
  }
}
