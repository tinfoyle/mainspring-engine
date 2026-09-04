package agents

import "encoding/json"

// ResultSchema is the provider-facing strict schema for ResultEnvelope. The
// application owns it; Account configuration cannot weaken required evidence,
// action, delegation, confidence, or Baseline invariants. Keep this schema at
// least as strict as ValidateResult: a provider response accepted here must not
// be discarded later merely because the two contracts disagree.
func ResultSchema() json.RawMessage {
	return append(json.RawMessage(nil), resultSchema...)
}

var resultSchema = json.RawMessage(`{
  "type":"object",
  "additionalProperties":false,
  "required":["contribution","findings","recommendations","questions","citations","proposed_actions","delegations","confidence","baseline"],
  "properties":{
    "contribution":{"type":"string","minLength":1,"maxLength":65536},
    "findings":{"type":"array","maxItems":100,"items":{"type":"string","minLength":1,"maxLength":4000}},
    "recommendations":{"type":"array","maxItems":100,"items":{"type":"string","minLength":1,"maxLength":4000}},
    "questions":{"type":"array","maxItems":100,"items":{"type":"string","minLength":1,"maxLength":4000}},
    "citations":{"type":"array","maxItems":100,"items":{"type":"object","additionalProperties":false,"required":["id","document_id","chunk_id","label"],"properties":{"id":{"type":"string","minLength":1,"maxLength":200},"document_id":{"type":"string","maxLength":200},"chunk_id":{"type":"string","maxLength":200},"label":{"type":"string","maxLength":500}}}},
    "proposed_actions":{"type":"array","maxItems":100,"items":{"type":"object","additionalProperties":false,"required":["kind","reason","payload","evidence"],"properties":{"kind":{"type":"string","pattern":"^[a-z][a-z0-9._:-]{0,127}$"},"reason":{"type":"string","minLength":3,"maxLength":4000},"payload":{"type":"object","additionalProperties":false,"properties":{}},"evidence":{"type":"array","items":{"type":"string","minLength":1,"maxLength":500}}}}},
    "delegations":{"type":"array","maxItems":32,"items":{"type":"object","additionalProperties":false,"required":["persona_id","request"],"properties":{"persona_id":{"type":"string","pattern":"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89aAbB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$"},"request":{"type":"string","minLength":3,"maxLength":4000}}}},
    "confidence":{"type":"string","enum":["low","medium","high"]},
    "baseline":{"anyOf":[
      {
        "type":"object",
        "additionalProperties":false,
        "required":["business_type","business_type_confidence","captured_topics","next_question_key","next_question","question_reason","automation_offers","approved_work","ready","readiness_reason","missing_topics"],
        "properties":{
          "business_type":{"type":"string","minLength":2,"maxLength":160},
          "business_type_confidence":{"type":"string","enum":["low","medium","high"]},
          "captured_topics":{"type":"array","maxItems":32,"items":{"type":"string","minLength":1,"maxLength":160}},
          "next_question_key":{"type":"string","pattern":"^baseline\\.[a-z][a-z0-9._:-]{0,118}$"},
          "next_question":{"type":"string","minLength":2,"maxLength":1000},
          "question_reason":{"type":"string","minLength":1,"maxLength":1000},
          "automation_offers":{"type":"array","maxItems":3,"items":{"type":"object","additionalProperties":false,"required":["key","title","description"],"properties":{"key":{"type":"string","pattern":"^[a-z][a-z0-9._:-]{0,127}$"},"title":{"type":"string","minLength":2,"maxLength":240},"description":{"type":"string","minLength":3,"maxLength":4000}}}},
          "approved_work":{"type":"array","maxItems":1,"items":{"type":"object","additionalProperties":false,"required":["key","title","description","priority"],"properties":{"key":{"type":"string","pattern":"^[a-z][a-z0-9._:-]{0,127}$"},"title":{"type":"string","minLength":2,"maxLength":240},"description":{"type":"string","minLength":3,"maxLength":20000},"priority":{"type":"string","enum":["low","normal","high","urgent"]}}}},
          "ready":{"type":"boolean","enum":[false]},
          "readiness_reason":{"type":"string","minLength":2,"maxLength":2000},
          "missing_topics":{"type":"array","maxItems":16,"items":{"type":"string","minLength":1,"maxLength":160}}
        }
      },
      {
        "type":"object",
        "additionalProperties":false,
        "required":["business_type","business_type_confidence","captured_topics","next_question_key","next_question","question_reason","automation_offers","approved_work","ready","readiness_reason","missing_topics"],
        "properties":{
          "business_type":{"type":"string","minLength":2,"maxLength":160},
          "business_type_confidence":{"type":"string","enum":["medium","high"]},
          "captured_topics":{"type":"array","minItems":4,"maxItems":32,"items":{"type":"string","minLength":1,"maxLength":160}},
          "next_question_key":{"type":"string","enum":[""]},
          "next_question":{"type":"string","enum":[""]},
          "question_reason":{"type":"string","enum":[""]},
          "automation_offers":{"type":"array","maxItems":3,"items":{"type":"object","additionalProperties":false,"required":["key","title","description"],"properties":{"key":{"type":"string","pattern":"^[a-z][a-z0-9._:-]{0,127}$"},"title":{"type":"string","minLength":2,"maxLength":240},"description":{"type":"string","minLength":3,"maxLength":4000}}}},
          "approved_work":{"type":"array","maxItems":1,"items":{"type":"object","additionalProperties":false,"required":["key","title","description","priority"],"properties":{"key":{"type":"string","pattern":"^[a-z][a-z0-9._:-]{0,127}$"},"title":{"type":"string","minLength":2,"maxLength":240},"description":{"type":"string","minLength":3,"maxLength":20000},"priority":{"type":"string","enum":["low","normal","high","urgent"]}}}},
          "ready":{"type":"boolean","enum":[true]},
          "readiness_reason":{"type":"string","minLength":2,"maxLength":2000},
          "missing_topics":{"type":"array","maxItems":0,"items":{"type":"string"}}
        }
      },
      {"type":"null"}
    ]}
  }
}`)
