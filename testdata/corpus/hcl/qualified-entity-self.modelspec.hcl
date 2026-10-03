# A qualified reference to the module's own name resolves to the module.
entity "Node" {
  key = ["id"]
  property "id" {
    type = "int"
  }
  property "parent" {
    entity = "qualified-entity-self.Node"
  }
}
