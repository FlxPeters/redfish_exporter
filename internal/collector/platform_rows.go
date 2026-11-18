//go:build !private_platforms && !platform_examples

package collector

// extraPlatforms is every row besides Default. This is meant to be a placeholder
// for the Containerfile to replace with a build tag. The actual per server_sku+firmware
// platform definitions exist in a private repository. Builing this repo with
// -tags private_platforms leaves this file out and compiles a definition supplied from outside
// this repository; if that outside definition is not supplied the build will fail. If
// wanting to just build the default, build without private_platforms.
//
// -tags platform_examples also leaves this file out: platform_examples_test.go then supplies
// the example rows, for tests only.
var extraPlatforms []Platform
