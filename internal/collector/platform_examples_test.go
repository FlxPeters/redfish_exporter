//go:build platform_examples

package collector

// extraPlatforms stands in for a private build's list of rows; platform_rows.go has the public,
// empty one, and -tags platform_examples leaves that file out. The platform table is then
// Default plus the example rows, the way a private build's table is Default plus its rows:
//
//	go test -tags platform_examples ./internal/...
//
// The examples are test files, so no binary carries them. A private build sets
// private_platforms instead and supplies its own extraPlatforms; setting both tags there would
// define extraPlatforms twice.
var extraPlatforms = []Platform{exampleSKUA, exampleSKUAFW2, exampleSKUB}
