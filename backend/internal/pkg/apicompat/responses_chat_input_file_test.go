package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesChatInputFile(t *testing.T) {
	const file = `{"type":"input_file","filename":"my notes.pdf","file_data":"data:application/pdf;base64,JVBERi0="}`
	const chatFile = `{"type":"file","file":{"filename":"my notes.pdf","file_data":"data:application/pdf;base64,JVBERi0="}}`
	for _, tc := range []struct{ name, input, want string }{
		{"between text", `[{"type":"message","role":"user","content":[{"type":"input_text","text":"read"},` + file + `,{"type":"input_text","text":"summarize"}]}]`, `[{"type":"text","text":"read"},` + chatFile + `,{"type":"text","text":"summarize"}]`},
		{"single part", `[{"role":"user","content":` + file + `}]`, `[` + chatFile + `]`},
		{"standalone", `[` + file + `]`, `[` + chatFile + `]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := ResponsesToChatCompletionsRequest(&ResponsesRequest{Input: json.RawMessage(tc.input)})
			require.NoError(t, err)
			require.Len(t, out.Messages, 1)
			require.JSONEq(t, tc.want, string(out.Messages[0].Content))
		})
	}
}

func TestResponsesChatInputFileNotRepresentable(t *testing.T) {
	for _, tc := range []struct{ name, role, fields, problem string }{
		{"unbound id", "user", `"file_id":"file-upload-1"`, "file_id"},
		{"URL", "user", `"file_url":"https://example.org/notes.pdf"`, "file_url"},
		{"missing", "user", `"filename":"notes.pdf"`, "file_data"},
		{"ambiguous", "user", `"file_data":"aGVsbG8=","file_id":"file-upload-1"`, "file_id"},
		{"wrong role", "assistant", `"file_data":"aGVsbG8="`, "assistant"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := `[{"role":"` + tc.role + `","content":[{"type":"input_text","text":"keep"},{"type":"input_file",` + tc.fields + `}]}]`
			out, err := ResponsesToChatCompletionsRequest(&ResponsesRequest{Input: json.RawMessage(input)})
			require.ErrorContains(t, err, tc.problem)
			require.Nil(t, out)
		})
	}
}
