package ext

import (
	"net/http"

	"github.com/refractionPOINT/lc-extension/core"
)

type BasicExtension struct {
	core.Extension
}

var extension *BasicExtension

// Boilerplate Code
// Serves the extension as a Cloud Function.
// ============================================================================
func init() {
	extension = &BasicExtension{}
	if err := extension.Init(); err != nil {
		panic(err)
	}
}

func Process(w http.ResponseWriter, r *http.Request) {
	extension.HandleRequest(w, r)
}

// Actual Extension Implementation
// ============================================================================
func (e *BasicExtension) Init() error {
	// Initialize the Extension core.
	if err := e.Extension.Init(); err != nil {
		return err
	}

	return nil
}
