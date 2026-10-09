# What is inside a heredoc is text: these brackets are not nesting.
record "A" {
  key = ["id"]
  field "id" {
    type    = "string"
    pattern = <<EOT
[([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([
EOT
  }
}
