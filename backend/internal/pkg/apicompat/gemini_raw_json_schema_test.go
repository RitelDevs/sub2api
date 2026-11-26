package apicompat

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGeminiRawJSONSchemaPreservesConstraints(t *testing.T) {
	const schema = `{"type":"object","properties":{"id":{"$ref":"#/$defs/id"}},"$defs":{"id":{"type":"integer","minimum":9007199254740993}},"required":["id"],"additionalProperties":false}`
	for _, fields := range []struct{ params, response string }{
		{"parametersJsonSchema", "responseJsonSchema"},
		{"parameters_json_schema", "response_json_schema"},
	} {
		t.Run(fields.params, func(t *testing.T) {
			body := fmt.Sprintf(`{"contents":[{"parts":[{"text":"hi"}]}],"tools":[{"functionDeclarations":[{"name":"lookup",%q:%s}]}],"generationConfig":{"responseMimeType":"application/json",%q:%s}}`, fields.params, schema, fields.response, schema)
			got, err := GeminiGenerateContentToChatCompletions("fixture", []byte(body), false)
			require.NoError(t, err)
			require.Equal(t, "json_schema", gjson.GetBytes(got, "response_format.type").String())
			require.Equal(t, schema, gjson.GetBytes(got, "tools.0.function.parameters").Raw)
			require.Equal(t, schema, gjson.GetBytes(got, "response_format.json_schema.schema").Raw)
		})
	}
}

func TestGeminiRawJSONSchemaRejectsUnrepresentableConfigurations(t *testing.T) {
	for _, tc := range []struct{ name, fields, want string }{
		{"tool-conflict", `"tools":[{"functionDeclarations":[{"name":"f","parameters":{},"parametersJsonSchema":{}}]}]`, "mutually exclusive"},
		{"response-conflict", `"generationConfig":{"responseMimeType":"application/json","responseSchema":{},"responseJsonSchema":{}}`, "mutually exclusive"},
		{"boolean-tool-schema", `"tools":[{"functionDeclarations":[{"name":"f","parametersJsonSchema":true}]}]`, "must be an object"},
		{"boolean-response-schema", `"generationConfig":{"responseMimeType":"application/json","responseJsonSchema":false}`, "must be an object"},
		{"incompatible-mime", `"generationConfig":{"responseMimeType":"text/plain","responseJsonSchema":{}}`, "requires responseMimeType"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"contents":[{"parts":[{"text":"hi"}]}],` + tc.fields + `}`
			got, err := GeminiGenerateContentToChatCompletions("fixture", []byte(body), false)
			require.Nil(t, got)
			require.ErrorContains(t, err, tc.want)
		})
	}
}
