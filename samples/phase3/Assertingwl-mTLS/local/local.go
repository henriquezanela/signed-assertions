package local

import (
	"log"

	"github.com/hpe-usp-spire/signed-assertions/phase3/Assertingwl-mTLS/options"
	api "github.com/hpe-usp-spire/signed-assertions/phase3/api-libs/global"
	alOps "github.com/hpe-usp-spire/signed-assertions/phase3/api-libs/options"
)

var Options *alOps.Options

func init() {
	log.Print("local init")
	// api-libs/options/options.go
	options, err := options.InitOptions()
	if err != nil {
		log.Fatal("Options init errored: ", err.Error())
	}

	Options = options
}

func InitGlobals() {
	log.Print("init global")

	// api-libs/global.go
	api.InitGlobals(Options)

}
