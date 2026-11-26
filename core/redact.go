package core

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/refractionPOINT/lc-extension/common"
)

// redactedMarker replaces a Secret value in an error the Extension logs or
// answers with. Fixed: never a prefix, a length or a hash of the value.
const redactedMarker = "[REDACTED]"

// minReplacedSecretLen is the shortest Secret value replaced in place. A text
// containing a shorter one (a PIN) is withheld whole instead: replacing it
// everywhere would mangle unrelated text.
const minReplacedSecretLen = 4

const withheldText = "details withheld: they may contain a Secret parameter"

// secretValues appends the values in data of the fields the schema declares
// Secret-typed, through lists and object fields (a JSON-object string too).
func secretValues(fields map[string]common.SchemaElement, data map[string]interface{}, out []string) []string {
	for k, v := range data {
		def, ok := fields[k]
		if !ok || v == nil {
			continue
		}
		items := []interface{}{v}
		if l, isList := v.([]interface{}); isList {
			items = l
		}
		for _, item := range items {
			if def.DataType == common.SchemaDataTypes.Secret {
				if s, ok := item.(string); ok && s != "" {
					out = append(out, s)
				}
				continue
			}
			if def.Object == nil {
				continue
			}
			var m map[string]interface{}
			switch o := item.(type) {
			case map[string]interface{}:
				m = o
			case string:
				if strings.HasPrefix(strings.TrimSpace(o), "{") {
					_ = json.Unmarshal([]byte(o), &m)
				}
			}
			if m != nil {
				out = secretValues(def.Object.Fields, m, out)
			}
		}
	}
	return out
}

// messageSecretValues is every Secret-typed value a message carries, by the
// Extension's schemas: request and fleet parameters, config, config being
// validated, and the org JWT.
// MessageSecretValues is messageSecretValues for code built on an Extension
// (the multiplexer classifies the envelope it forwards, after its hooks).
func (e *Extension) MessageSecretValues(m *common.Message) []string {
	return e.messageSecretValues(m)
}

func (e *Extension) messageSecretValues(m *common.Message) []string {
	var out []string
	if r := m.Request; r != nil {
		out = appendOrgJWT(out, r.Org.JWT)
		if def, ok := e.RequestSchema[r.Action]; ok {
			out = secretValues(def.ParameterDefinitions.Fields, r.Data, out)
		}
		out = secretValues(e.ConfigSchema.Fields, r.Config, out)
	}
	if f := m.FleetRequest; f != nil {
		if def, ok := e.RequestSchema[f.Action]; ok {
			out = secretValues(def.ParameterDefinitions.Fields, f.Data, out)
		}
	}
	if c := m.ConfigValidation; c != nil {
		out = appendOrgJWT(out, c.Org.JWT)
		out = secretValues(e.ConfigSchema.Fields, c.Config, out)
	}
	if ev := m.Event; ev != nil {
		out = appendOrgJWT(out, ev.Org.JWT)
		out = secretValues(e.ConfigSchema.Fields, ev.Config, out)
	}
	return out
}

// ScrubSecrets is scrubSecrets for code built on an Extension.
func ScrubSecrets(s string, secrets []string) string {
	return scrubSecrets(s, secrets)
}

// scrubSecrets replaces every occurrence of a value of secrets in s with
// redactedMarker, longest first; a text containing a value too short to
// replace is withheld whole.
func scrubSecrets(s string, secrets []string) string {
	if s == "" || len(secrets) == 0 {
		return s
	}
	// Each value as is and in its JSON-escaped and Go-quoted forms.
	var sorted []string
	for _, v := range secrets {
		if v == "" {
			continue
		}
		sorted = append(sorted, v)
		if b, err := json.Marshal(v); err == nil {
			if f := string(b[1 : len(b)-1]); f != v {
				sorted = append(sorted, f)
			}
		}
		if f := strconv.Quote(v); f[1:len(f)-1] != v {
			sorted = append(sorted, f[1:len(f)-1])
		}
	}
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
	for _, v := range sorted {
		if !strings.Contains(s, v) {
			continue
		}
		if len(v) < minReplacedSecretLen {
			return withheldText
		}
		s = strings.ReplaceAll(s, v, redactedMarker)
	}
	return s
}

// appendOrgJWT appends an org JWT. A JWT is never a few characters: a shorter
// value is not one (a placeholder) and would only make texts withheld.
func appendOrgJWT(out []string, jwt string) []string {
	if len(jwt) < minReplacedSecretLen {
		return out
	}
	return append(out, jwt)
}
