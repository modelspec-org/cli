# What is inside a heredoc is text: these brackets are not nesting.
entity "A" {
  key = ["id"]
  property "id" {
    type    = "string"
    pattern = <<EOT
[([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([([
EOT
  }
}
