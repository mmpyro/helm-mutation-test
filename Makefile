BINARY  := bin/helm-mutation-test
FIXTURE := testdata/charts/sample

.DEFAULT_GOAL := build

.PHONY: build
build: ## Build the plugin binary
	CGO_ENABLED=0 go build -trimpath -o $(BINARY) ./cmd/helm-mutation-test

.PHONY: test
test: ## Run the full test suite with the race detector
	go test ./... -race

.PHONY: test-short
test-short: ## Run tests without the race detector
	go test ./...

.PHONY: lint
lint: ## Vet and check formatting
	go vet ./...
	@unformatted=$$(gofmt -l cmd internal); \
	if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi

.PHONY: demo
demo: build ## Score the fixture chart with both suites, to show the difference
	@echo "=== weak suite (expected to score badly) ==="
	-@$(BINARY) -f 'tests/weak_test.yaml' $(FIXTURE)
	@echo "=== strong suite (expected to score well) ==="
	-@$(BINARY) -f 'tests/strong_test.yaml' $(FIXTURE)

.PHONY: install
install: build ## Install as a Helm plugin from this directory
	helm plugin install . || helm plugin update mutation-test

.PHONY: clean
clean:
	rm -rf bin .helm-mutation-test

.PHONY: help
help:
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "};{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
