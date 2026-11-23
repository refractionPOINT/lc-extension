package simplified

import (
	"encoding/json"
	"errors"
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
			fastJSON, err := json.Marshal(limacharlie.Dict{lookupDataKey: fast})
			require.NoError(t, err)
			slowJSON, err := json.Marshal(limacharlie.Dict{lookupDataKey: slow})
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

// fakeLookupCleaner serves the lookups in stored and answers a removal with
// the error in removeErrs for that key, if any.
type fakeLookupCleaner struct {
	stored     limacharlie.HiveConfigData
	removeErrs map[string]error
	removed    []string
}

func (h *fakeLookupCleaner) Remove(args limacharlie.HiveArgs) (interface{}, error) {
	if err, ok := h.removeErrs[args.Key]; ok {
		return nil, err
	}
	h.removed = append(h.removed, args.Key)
	return nil, nil
}

func (h *fakeLookupCleaner) ListMtd(args limacharlie.HiveArgs) (limacharlie.HiveConfigData, error) {
	return h.stored, nil
}

const (
	testLookupRule  = "ext-test-update"
	testLookupTag   = "ext:test"
	testLookupOurs1 = "ours-1"
	testLookupOurs2 = "ours-2"
)

func newTestLookupCleaner(removeErrs map[string]error) *fakeLookupCleaner {
	tagged := limacharlie.HiveData{UsrMtd: limacharlie.UsrMtd{Tags: []string{testLookupTag}}}
	return &fakeLookupCleaner{
		stored: limacharlie.HiveConfigData{
			testLookupOurs1: tagged,
			testLookupOurs2: tagged,
			"theirs":        {UsrMtd: limacharlie.UsrMtd{Tags: []string{"ext:other"}}},
		},
		removeErrs: removeErrs,
	}
}

func newTestLookupExtension() *LookupExtension {
	return &LookupExtension{
		Name:     "test",
		Logger:   &recordingLogger{},
		tag:      testLookupTag,
		ruleName: testLookupRule,
	}
}

func recordNotFoundErr(key string) error {
	return errors.New("api error: 400 Bad Request: lc_error_code:RECORD_NOT_FOUND - record name '" + key + "'")
}

func TestRemoveOrgResourcesSucceedsWhenRecordsAreAlreadyGone(t *testing.T) {
	for _, tc := range []struct {
		name        string
		gone        []string
		wantRemoved []string
	}{
		{"rule gone", []string{testLookupRule}, []string{testLookupOurs1, testLookupOurs2}},
		{"lookup gone", []string{testLookupOurs1}, []string{testLookupRule, testLookupOurs2}},
		{"rule and lookups gone", []string{testLookupRule, testLookupOurs1, testLookupOurs2}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			removeErrs := map[string]error{}
			for _, key := range tc.gone {
				removeErrs[key] = recordNotFoundErr(key)
			}
			h := newTestLookupCleaner(removeErrs)
			l := newTestLookupExtension()

			require.NoError(t, l.removeOrgResources(h, "oid"))
			require.ElementsMatch(t, tc.wantRemoved, h.removed)
			require.Empty(t, l.Logger.(*recordingLogger).errors)
		})
	}
}

func TestRemoveOrgResourcesFailsOnOtherRemoveErrors(t *testing.T) {
	denied := errors.New("api error: 403 Forbidden: lc_error_code:UNAUTHORIZED")
	for _, tc := range []struct {
		name string
		key  string
	}{
		{"rule", testLookupRule},
		{"lookup", testLookupOurs1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestLookupCleaner(map[string]error{tc.key: denied})
			l := newTestLookupExtension()

			err := l.removeOrgResources(h, "oid")
			require.ErrorIs(t, err, denied)
			require.ErrorContains(t, err, tc.key)
			require.Len(t, l.Logger.(*recordingLogger).errors, 1)
		})
	}
}
