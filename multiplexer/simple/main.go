package main

import (
	"github.com/refractionPOINT/lc-extension/multiplexer"
	"github.com/refractionPOINT/lc-extension/server/webserver"
)

func main() {
	// Start processing.
	if err := multiplexer.Extension.Init(); err != nil {
		panic(err)
	}

	webserver.RunExtension(multiplexer.Extension)
}
