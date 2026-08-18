package agents

import "encoding/json"

// ResultSchema is the provider-facing strict schema for ResultEnvelope. The
// application owns it; Account configuration cannot weaken required evidence,
// action, delegation, or confidence fields.
func ResultSchema() json.RawMessage {
	return append(json.RawMessage(nil), resultSchema...)
}

var resultSchema = json.RawMessage(`{
  "type":"object",
  "additionalProperties":false,
  "required":["contribution","findings","recommendations","questions","citations","proposed_actions","delegations","confidence"],
  "properties":{
    "contribution":{"type":"string","minLength":1,"maxLength":65536},
    "findings":{"type":"array","maxItems":100,"items":{"type":"string","minLength":1,"maxLength":4000}},
    "recommendations":{"type":"array","maxItems":100,"items":{"type":"string","minLength":1,"maxLength":4000}},
    "questions":{"type":"array","maxItems":100,"items":{"type":"string","minLength":1,"maxLength":4000}},
    "citations":{"type":"array","maxItems":100,"items":{"type":"object","additionalProperties":false,"required":["id","document_id","chunk_id","label"],"properties":{"id":{"type":"string","minLength":1,"maxLength":200},"document_id":{"type":"string","maxLength":200},"chunk_id":{"type":"string","maxLength":200},"label":{"type":"string","maxLength":500}}}},
    "proposed_actions":{"type":"array","maxItems":100,"items":{"type":"object","additionalProperties":false,"required":["kind","reason","payload","evidence"],"properties":{"kind":{"type":"string","minLength":1,"maxLength":128},"reason":{"type":"string","minLength":3,"maxLength":4000},"payload":{"type":"object","additionalProperties":false,"properties":{}},"evidence":{"type":"array","items":{"type":"string","minLength":1,"maxLength":500}}}}},
    "delegations":{"type":"array","maxItems":32,"items":{"type":"object","additionalProperties":false,"required":["persona_id","request"],"properties":{"persona_id":{"type":"string","minLength":36,"maxLength":36},"request":{"type":"string","minLength":3,"maxLength":4000}}}},
    "confidence":{"type":"string","enum":["low","medium","high"]}
  }
}`)
