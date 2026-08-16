package todo

import (
	"encoding/json"
	"strings"
)

// AsyncAPI contract extraction — event-driven contracts (Kafka, AMQP, MQTT). An AsyncAPI document is
// structurally close to OpenAPI (JSON or YAML), so it reuses the same schema resolution: each
// (channel, operation) is an endpoint keyed PUB/SUB + channel, its signature the property names of
// the message payload. AsyncAPI 2.x shape (publish/subscribe under a channel); the payload may be
// inline or a $ref to a component message or schema.

type asyncSpec struct {
	Channels   map[string]asyncChannel `json:"channels"`
	Components struct {
		Schemas  map[string]oaSchema     `json:"schemas"`
		Messages map[string]asyncMessage `json:"messages"`
	} `json:"components"`
}

type asyncChannel struct {
	Publish   *asyncOp `json:"publish"`
	Subscribe *asyncOp `json:"subscribe"`
}

type asyncOp struct {
	Message asyncMessage `json:"message"`
}

type asyncMessage struct {
	Ref     string   `json:"$ref"`
	Payload oaSchema `json:"payload"`
}

// isAsyncAPI reports whether a JSON document is an AsyncAPI one rather than OpenAPI — it carries the
// asyncapi version field (and channels), where OpenAPI carries openapi/paths.
func isAsyncAPI(b []byte) bool {
	var probe struct {
		AsyncAPI string                     `json:"asyncapi"`
		Channels map[string]json.RawMessage `json:"channels"`
	}
	_ = json.Unmarshal(b, &probe)
	return len(probe.AsyncAPI) > 0 || len(probe.Channels) > 0
}

func asyncEndpoints(b []byte) (map[string]Endpoint, error) {
	var s asyncSpec
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	out := map[string]Endpoint{}
	for ch, c := range s.Channels {
		for method, op := range map[string]*asyncOp{"PUB": c.Publish, "SUB": c.Subscribe} {
			if op == nil {
				continue
			}
			// A message carries one shape; the payload's fields are the signature (kept in Response).
			e := Endpoint{Method: method, Path: ch, Response: sortUniq(asyncPayloadProps(op.Message, &s))}
			out[e.key()] = e
		}
	}
	return out, nil
}

func asyncPayloadProps(m asyncMessage, s *asyncSpec) []string {
	if len(m.Ref) > 0 {
		name := m.Ref[strings.LastIndex(m.Ref, "/")+1:]
		if cm, ok := s.Components.Messages[name]; ok {
			m = cm
		}
	}
	return schemaProps(m.Payload, s.Components.Schemas)
}
