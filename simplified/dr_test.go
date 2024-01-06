package simplified

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
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
