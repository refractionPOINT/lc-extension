package simplified

import (
	"encoding/json"
	"testing"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/stretchr/testify/require"
)

// asDict replaces an ImportFromStruct round-trip on the hot path, so the
// payload it yields must marshal identically to what the round-trip produced.
func TestAsDictMatchesImportFromStruct(t *testing.T) {
	lookup := limacharlie.Dict{
		"d41d8cd98f00b204e9800998ecf8427e": limacharlie.Dict{
			"Id":        "87752fb8-e9f6-4235-91e2-c4343677d817",
			"MitreID":   "T1068",
			"Category":  "vulnerable driver",
			"Commands":  map[string]interface{}{"Command": "sc.exe create", "Usecase": "elevate"},
			"Resources": []interface{}{"https://example.test/a", "https://example.test/b"},
			"Tags":      []string{"driver.sys", "DRIVER.SYS"},
		},
		"da39a3ee5e6b4b0d3255bfef95601890afd80709": limacharlie.Dict{
			"Id":        "9bf033e4-7295-4b63-8772-638b76851741",
			"MitreID":   "",
			"Category":  "",
			"Commands":  nil,
			"Resources": []interface{}{},
			"Tags":      []string{},
		},
	}

	for _, tc := range []struct {
		name string
		in   interface{}
	}{
		{"limacharlie.Dict", lookup},
		{"map[string]interface{}", map[string]interface{}(lookup)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fast, ok := asDict(tc.in)
			require.True(t, ok, "expected fast path to accept %T", tc.in)

			slow := limacharlie.Dict{}
			_, err := slow.ImportFromStruct(tc.in)
			require.NoError(t, err)

			// Compare marshalled forms: the round-trip normalises Go types
			// (e.g. []string -> []interface{}), so the maps are not
			// DeepEqual even though the emitted payload is the same.
			fastJSON, err := json.Marshal(limacharlie.Dict{"lookup_data": fast})
			require.NoError(t, err)
			slowJSON, err := json.Marshal(limacharlie.Dict{"lookup_data": slow})
			require.NoError(t, err)
			require.JSONEq(t, string(slowJSON), string(fastJSON))
		})
	}
}

// Anything that is not already a map must fall through so onUpdate still
// coerces it via ImportFromStruct.
func TestAsDictRejectsNonMaps(t *testing.T) {
	for _, v := range []interface{}{
		[]interface{}{"a", "b"},
		struct{ A string }{A: "b"},
		&struct{ A string }{A: "b"},
		"a string",
		42,
		nil,
		map[string]string{"a": "b"},
	} {
		_, ok := asDict(v)
		require.False(t, ok, "expected fall-through for %T", v)
	}
}
