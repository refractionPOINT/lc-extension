package simplified

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/stretchr/testify/require"
)

func TestSuppression(t *testing.T) {
	a := []byte(`{"respond":[{"action":"report", "name": "XXX"}]}`)
	d := limacharlie.Dict{}
	if err := json.Unmarshal(a, &d); err != nil {
		panic(err)
	}

	final := fmt.Sprintf("%#v", addSuppression(d, "1h"))
	expected := `limacharlie.Dict{"respond":[]interface {}{map[string]interface {}{"action":"report", "name":"XXX", "suppression":limacharlie.Dict{"is_global":false, "keys":[]string{"XXX"}, "max_count":1, "period":"1h"}}}}`
	if final != expected {
		t.Errorf("unexpected suppression: %s\n!=\n%s", final, expected)
	}
}

func TestLogFailedRuleRemovalsSkipsRulesAlreadyGone(t *testing.T) {
	logger := &recordingLogger{}

	logFailedRuleRemovals(logger, []limacharlie.BatchResponse{
		{},
		{Error: "lc_error_code:RECORD_NOT_FOUND - record name 'dr-general:oid:gone'"},
		{Error: "lc_error_code:UNAUTHORIZED"},
	})

	require.Equal(t, []string{"failed to remove rule: lc_error_code:UNAUTHORIZED"}, logger.errors)
}
