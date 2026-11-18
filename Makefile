.PHONY: benchmark build test mock-test capture clean help lint fmt fmt-check gotests unit-test platform-examples-test integration-test coverage check ci perf-mock perf-live perf-help validate-live serve-live

# Default target
help:
	@echo "Available targets:"
	@echo "  build       - Build the redfish_exporter binary"
	@echo "  test        - Run all tests"
	@echo "  unit-test   - Run unit tests only"
	@echo "  platform-examples-test - Run the tests with the example rows in the platform table"
	@echo "  integration-test - Run integration tests"
	@echo "  lint        - Run golangci-lint"
	@echo "  fmt         - Format code with gofmt"
	@echo "  fmt-check   - Check if code is formatted"
	@echo "  coverage    - Generate and show test coverage"
	@echo "  check       - Run fmt, lint, and test (dev workflow)"
	@echo "  ci          - Run all CI checks"
	@echo "  mock-test   - Run mock server with exporter for testing"
	@echo "  validate-live - Scrape a live system and assert metrics are healthy"
	@echo "  serve-live  - Run the exporter locally against live systems (no tests, stays up)"
	@echo "  capture     - Capture Redfish data from a BMC"
	@echo "  perf-mock   - Run performance analysis against mock server"
	@echo "  perf-live   - Run performance analysis against live system"
	@echo "  perf-compare - Compare mock vs live performance"
	@echo "  perf-help   - Show performance analysis help"
	@echo "  clean       - Clean build artifacts"
	@echo ""
	@echo "For mock-test, you can specify TESTDATA:"
	@echo "  make mock-test TESTDATA=gb300/gb300_host.txt"
	@echo ""
	@echo "For capture, specify HOST, USER, PASS, and OUTPUT:"
	@echo "  make capture HOST=10.0.0.1 USER=admin PASS=password OUTPUT=mysystem"
	@echo ""
	@echo "For validate-live, specify TARGET (and optionally MODULES + credentials):"
	@echo "  make validate-live TARGET=10.0.0.1"
	@echo "  REDFISH_USER=admin REDFISH_PASS=pass make validate-live TARGET=10.0.0.1 MODULES=powershelf"
	@echo ""
	@echo "For serve-live, TARGET is optional (it only shapes the scrape URL printed for you):"
	@echo "  REDFISH_USER=admin REDFISH_PASS=pass make serve-live TARGET=10.0.0.1"
	@echo "  LOGLEVEL=debug REDFISH_USER=admin REDFISH_PASS=pass make serve-live TARGET=10.0.0.1"

# Build the exporter
build:
	go build -o redfish_exporter ./cmd/redfish-exporter

# Run tests and benchmarks
test: gotests benchmark

gotests: unit-test

# Run mock server and exporter for testing
mock-test:
	@if [ -z "$(TESTDATA)" ]; then \
		echo "Using default test data: gb300/gb300_host.txt"; \
		echo "You can specify different data with: make mock-test TESTDATA=<path>"; \
		./tools/mock-server/test-local.sh gb300/gb300_host.txt; \
	else \
		./tools/mock-server/test-local.sh $(TESTDATA); \
	fi

# Build and run the capture tool
capture:
	@if [ -z "$(HOST)" ] || [ -z "$(USER)" ] || [ -z "$(PASS)" ] || [ -z "$(OUTPUT)" ]; then \
		echo "Error: Required parameters missing"; \
		echo "Usage: make capture HOST=<bmc-ip> USER=<username> PASS=<password> OUTPUT=<name>"; \
		echo ""; \
		echo "Optional parameters:"; \
		echo "  TIMEOUT=30s            - Request timeout (default: 10s)"; \
		echo "  SLEEP=100              - Sleep between requests in ms (default: 0)"; \
		echo "  MAX=100                - Max endpoints to capture (default: unlimited)"; \
		echo "  INSECURE=false         - Skip TLS verification (default: true)"; \
		echo "  SKIP_PATHS=Actions,Logs - Comma-separated paths to skip (default: Actions)"; \
		exit 1; \
	fi
	@echo "Building capture tool..."
	@cd tools/capture && go build -o capture main.go
	@echo "Capturing from $(HOST)..."
	@cd tools/capture && ./capture \
		-host $(HOST) \
		-user $(USER) \
		-pass $(PASS) \
		-output $(OUTPUT) \
		$(if $(TIMEOUT),-timeout $(TIMEOUT)) \
		$(if $(SLEEP),-sleep $(SLEEP)) \
		$(if $(MAX),-max $(MAX)) \
		$(if $(filter false,$(INSECURE)),-insecure=false) \
		$(if $(SKIP_PATHS),-skipPaths "$(SKIP_PATHS)")
	@echo ""
	@echo "Capture complete! To test with mock server:"
	@echo "  make mock-test TESTDATA=$(OUTPUT)/capture.txt"

# Validate the exporter against a live Redfish system
validate-live:
	@if [ -z "$(TARGET)" ]; then \
		echo "Error: TARGET is required"; \
		echo "Usage: make validate-live TARGET=<ip-or-host[:port]>"; \
		echo ""; \
		echo "Optional parameters:"; \
		echo "  MODULES=powershelf,chassis  - Modules to request (default: built-in bundle; powershelf NOT included)"; \
		echo "  REDFISH_USER=admin          - Username (with REDFISH_PASS, generates a temp config)"; \
		echo "  REDFISH_PASS=pass           - Password"; \
		echo "  CONFIG_FILE=config.yml      - Existing config to use when no credentials are given"; \
		echo "  EXPORTER_PORT=9613          - Local exporter port"; \
		echo ""; \
		echo "Examples:"; \
		echo "  make validate-live TARGET=192.168.1.100"; \
		echo "  REDFISH_USER=admin REDFISH_PASS=pass make validate-live TARGET=192.168.1.100 MODULES=powershelf"; \
		exit 1; \
	fi
	@TARGET="$(TARGET)" MODULES="$(MODULES)" REDFISH_USER="$(REDFISH_USER)" REDFISH_PASS="$(REDFISH_PASS)" \
		CONFIG_FILE="$(CONFIG_FILE)" EXPORTER_PORT="$(EXPORTER_PORT)" \
		./tools/validate-live/validate.sh

# Run the exporter locally against live Redfish systems, and leave it running.
# Unlike validate-live this asserts nothing and scrapes nothing — it just serves, so you can
# drive real BMCs by hand and watch the logs.
serve-live:
	@TARGET="$(TARGET)" MODULES="$(MODULES)" REDFISH_USER="$(REDFISH_USER)" REDFISH_PASS="$(REDFISH_PASS)" \
		CONFIG_FILE="$(CONFIG_FILE)" EXPORTER_PORT="$(EXPORTER_PORT)" LOGLEVEL="$(LOGLEVEL)" \
		./tools/serve-live/serve.sh

# Clean build artifacts
clean:
	rm -f redfish_exporter
	rm -f tools/mock-server/exporter-config.yml
	rm -f tools/capture/capture
	rm -f coverage.out

# Run unit tests only (exclude integration)
unit-test:
	go test -v -race -count=1 ./...

# Run the tests with the example rows in the platform table
platform-examples-test:
	go test -race -count=1 -tags platform_examples ./internal/...

# Run integration tests
integration-test:
	go test -v -run Integration ./internal/collector

# Run linter
lint:
	golangci-lint run ./...
	golangci-lint run --build-tags platform_examples ./internal/...

# Format code
fmt:
	gofmt -w -s .
	go mod tidy

# Check formatting
fmt-check:
	@test -z "$$(gofmt -l .)" || (echo "Please run 'make fmt'" && exit 1)

# Generate test coverage
coverage:
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

# Development workflow - format, lint, and test
check: fmt lint test

# CI workflow - stricter checks
ci: fmt-check lint test platform-examples-test build

# Performance analysis against mock server
perf-mock:
	@echo "Running performance analysis against mock server..."
	@./tools/performance-analysis/run-analysis.sh mock localhost:8443

# Performance analysis against live system
perf-live:
	@if [ -z "$(TARGET)" ]; then \
		echo "Error: TARGET is required"; \
		echo "Usage: make perf-live TARGET=<ip-or-hostname>"; \
		echo ""; \
		echo "Optional parameters:"; \
		echo "  RUNS=10             - Number of test runs (default: 5)"; \
		echo "  VERBOSE=1           - Enable verbose output"; \
		echo "  REDFISH_USER=admin  - Username for Redfish API"; \
		echo "  REDFISH_PASS=pass   - Password for Redfish API"; \
		echo ""; \
		echo "Examples:"; \
		echo "  # Using config.yml"; \
		echo "  make perf-live TARGET=192.168.1.100"; \
		echo ""; \
		echo "  # Using environment credentials"; \
		echo "  REDFISH_USER=admin REDFISH_PASS=password make perf-live TARGET=192.168.1.100"; \
		exit 1; \
	fi
	@echo "Running performance analysis against $(TARGET)..."
	@REDFISH_USER="$(REDFISH_USER)" REDFISH_PASS="$(REDFISH_PASS)" \
		./tools/performance-analysis/run-analysis.sh \
		$(if $(RUNS),-r $(RUNS)) \
		$(if $(VERBOSE),-v) \
		live $(TARGET)

# Show performance analysis help
perf-help:
	@echo "Performance Analysis Usage:"
	@echo ""
	@echo "Test against mock server:"
	@echo "  make perf-mock"
	@echo ""
	@echo "Test against live system (using config.yml):"
	@echo "  make perf-live TARGET=192.168.1.100"
	@echo ""
	@echo "Test against live system (with credentials):"
	@echo "  REDFISH_USER=admin REDFISH_PASS=password make perf-live TARGET=192.168.1.100"
	@echo ""
	@echo "Run multiple iterations:"
	@echo "  make perf-live TARGET=192.168.1.100 RUNS=10"
	@echo ""
	@echo "Enable verbose output:"
	@echo "  make perf-live TARGET=192.168.1.100 VERBOSE=1"
	@echo ""
	@echo "Credentials:"
	@echo "  - By default, uses config.yml in project root"
	@echo "  - Can override with REDFISH_USER and REDFISH_PASS environment variables"
	@echo "  - Temporary config is created and cleaned up automatically"
	@echo ""
	@echo "The performance tool will:"
	@echo "  - Measure scrape duration"
	@echo "  - Count metrics collected"
	@echo "  - Calculate statistics (min/max/avg/percentiles)"
	@echo "  - Identify performance bottlenecks"

# Compare mock vs live performance
perf-compare:
	@if [ -z "$(TARGET)" ]; then \
		echo "Error: TARGET is required for live system"; \
		echo "Usage: make perf-compare TARGET=<ip-or-hostname>"; \
		echo ""; \
		echo "Optional parameters:"; \
		echo "  RUNS=5              - Number of test runs (default: 3)"; \
		echo "  REDFISH_USER=admin  - Username for Redfish API"; \
		echo "  REDFISH_PASS=pass   - Password for Redfish API"; \
		echo ""; \
		echo "Example:"; \
		echo "  REDFISH_USER=admin REDFISH_PASS=password make perf-compare TARGET=192.168.1.100"; \
		exit 1; \
	fi
	@echo "Comparing mock vs live performance..."
	@echo "This will run tests against both mock server and $(TARGET)"
	@echo ""
	@# Build analysis tool if needed
	@cd tools/performance-analysis && go build -o analyze analyze.go
	@# Start mock server if needed
	@if ! curl -k -s "https://localhost:8443/redfish/v1/" > /dev/null 2>&1; then \
		echo "Starting mock server with HTTPS on port 8443..."; \
		cd tools/mock-server && go build -o mock-server main.go && \
		./mock-server -https-port 8443 -auth auth.yml -file testdata/gb300/gb300_host.txt > /dev/null 2>&1 & \
		MOCK_PID=$$!; \
		sleep 2; \
	fi
	@# Setup configs and run comparison
	@if [ ! -z "$(REDFISH_USER)" ] && [ ! -z "$(REDFISH_PASS)" ]; then \
		echo "Creating config for live system..."; \
		echo "hosts:" > tools/performance-analysis/temp-live-config.yml; \
		echo "  default:" >> tools/performance-analysis/temp-live-config.yml; \
		echo "    username: $(REDFISH_USER)" >> tools/performance-analysis/temp-live-config.yml; \
		echo "    password: $(REDFISH_PASS)" >> tools/performance-analysis/temp-live-config.yml; \
		echo "  $(TARGET):" >> tools/performance-analysis/temp-live-config.yml; \
		echo "    username: $(REDFISH_USER)" >> tools/performance-analysis/temp-live-config.yml; \
		echo "    password: $(REDFISH_PASS)" >> tools/performance-analysis/temp-live-config.yml; \
		echo "loglevel: info" >> tools/performance-analysis/temp-live-config.yml; \
		echo "Starting exporters with proper configs..."; \
		pkill -f redfish_exporter 2>/dev/null || true; \
		sleep 1; \
		echo "  Starting mock exporter on :9610..."; \
		./redfish_exporter --config.file=tools/mock-server/exporter-config.yml --web.listen-address=:9610 > /dev/null 2>&1 & \
		MOCK_EXPORTER_PID=$$!; \
		echo "  Starting live exporter on :9611..."; \
		./redfish_exporter --config.file=tools/performance-analysis/temp-live-config.yml --web.listen-address=:9611 > /dev/null 2>&1 & \
		LIVE_EXPORTER_PID=$$!; \
		sleep 2; \
		echo "Running comparison..."; \
		cd tools/performance-analysis && ./analyze -compare -mock localhost:8443 -live $(TARGET) -mock-endpoint http://localhost:9610 -live-endpoint http://localhost:9611 -runs $(if $(RUNS),$(RUNS),3); \
		pkill -f redfish_exporter 2>/dev/null || true; \
		rm -f temp-live-config.yml; \
	else \
		echo "Using existing config.yml..."; \
		cd tools/performance-analysis && ./analyze -compare -live $(TARGET) -runs $(if $(RUNS),$(RUNS),3); \
	fi
	@# Cleanup mock if we started it
	@if [ ! -z "$$MOCK_PID" ]; then \
		kill $$MOCK_PID 2>/dev/null || true; \
	fi

benchmark:
	@cd internal/collector ; \
	go test -bench=.
