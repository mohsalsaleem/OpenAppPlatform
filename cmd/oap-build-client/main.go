// oap-build-client implements the command builder contract without host keys.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/source"
	"io"
	"os"
)

func main() {
	var request source.BuildRequest
	d := json.NewDecoder(io.LimitReader(os.Stdin, 8193))
	d.DisallowUnknownFields()
	if d.Decode(&request) != nil {
		fail()
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		fail()
	}
	result, e := (source.HTTPBuilder{URL: os.Getenv("OAP_BUILD_URL"), Token: os.Getenv("OAP_BUILD_TOKEN")}).Build(context.Background(), request)
	if e != nil {
		fail()
	}
	if json.NewEncoder(os.Stdout).Encode(result) != nil {
		fail()
	}
}
func fail() {
	fmt.Fprintln(os.Stderr, "Trusted host builder outcome is uncertain; inspect its receipt before retrying.")
	os.Exit(1)
}
