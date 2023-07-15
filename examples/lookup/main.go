package ext

import (
	"context"
	"net/http"

	"github.com/refractionPOINT/go-limacharlie/limacharlie"
	"github.com/refractionPOINT/lc-extension/core"
	"github.com/refractionPOINT/lc-extension/simplified"
)

// The singleton reference to this Extension running.
var Extension *core.Extension

// Boilerplate Code
// Serves the extension as a Cloud Function.
// ============================================================================
func init() {
	ext := &simplified.LookupExtension{
		Name:      "example-lookup",
		SecretKey: "1234",
		Logger:    &limacharlie.LCLoggerGCP{},
		GetLookup: generateLookup,
	}

	var err error
	if Extension, err = ext.Init(); err != nil {
		panic(err)
	}
}

func generateLookup(ctx context.Context) (simplified.LookupData, error) {
	// This is where we generate a list of lookups and their data
	// that we want set by this extension.
	return simplified.LookupData{
		"example-lookup-1": limacharlie.Dict{
			"some": limacharlie.Dict{
				"mtd":      "some source",
				"priority": 1,
			},
			"some2": limacharlie.Dict{
				"mtd":      "another source",
				"priority": 4,
			},
		},
		"example-lookup-2": limacharlie.Dict{
			"item": limacharlie.Dict{
				"mtd":      "some source",
				"priority": 1,
			},
			"item2": limacharlie.Dict{
				"mtd":      "some source",
				"priority": 1,
			},
		},
	}, nil
}

// This example defines a simple http handler that can now be used
// as an entry point to a Cloud Function. See /server/webserver for a
// useful helper to run the handler as a webserver in a container.
func Process(w http.ResponseWriter, r *http.Request) {
	Extension.ServeHTTP(w, r)
}
