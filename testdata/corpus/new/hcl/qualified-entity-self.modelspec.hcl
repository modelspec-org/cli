# A qualified reference to the module's own name resolves to the module.
record "Node" {
  key = ["id"]
  field "id" {
    type = "int"
  }
  field "parent" {
    record = "qualified-entity-self.Node"
  }
}
