package llm

const SchemaDefinition = `{
  "type": "object",
  "properties": {
    "dockerfile": { "type": "string" },
    "expose_port": { "type": "integer" },
    "build_args": {
      "type": "object",
      "additionalProperties": { "type": "string" }
    },
    "support_files": {
      "type": "object",
      "additionalProperties": { "type": "string" }
    },
    "reasoning": { "type": "string" }
  },
  "required": ["dockerfile", "expose_port"]
}`
