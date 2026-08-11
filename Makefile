BINARY  := bin/helm-mutation-test
FIXTURE := testdata/charts/sample
PLUGIN  := mutation-test

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

# -count=1 because these tests exec a binary and touch the filesystem: a cached
# PASS would be reporting on a run that never happened. The package builds the
# binary itself, so this target deliberately does not depend on `build`.
.PHONY: integration-tests
integration-tests: ## Run the black-box CLI integration tests
	go test -tags=integration -count=1 -timeout 15m ./test/integration/...

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

# `helm plugin install .` on a local path uses helm's LocalInstaller, which *symlinks* this
# directory into HELM_PLUGINS under its own basename. The plugin directory is therefore the
# working tree, and the `build` above is the whole refresh -- helm's own LocalInstaller.Update
# is a no-op ("local repository is auto-updated").
#
# Do not fall back to `helm plugin update`: that goes through installer.FindSource ->
# existingVCSRepo -> vcs.NewRepo, which runs `git config --get remote.origin.url` and fails
# with "Unable to retrieve local repo information" on any checkout lacking an `origin` remote.
.PHONY: install
install: build ## Install as a Helm plugin from this directory (safe to re-run)
	@link="$$(helm env HELM_PLUGINS)/$(notdir $(CURDIR))"; \
	if [ -L "$$link" ] && [ "$$(cd -P "$$link" 2>/dev/null && pwd)" = "$$(cd -P '$(CURDIR)' && pwd)" ]; then \
		echo "$(PLUGIN) is already linked to $(CURDIR); rebuilt $(BINARY)."; \
	elif helm plugin list | awk 'NR > 1 { print $$1 }' | grep -qx '$(PLUGIN)'; then \
		echo "$(PLUGIN) is already installed from another location ($$link is not a link to this tree)." >&2; \
		echo "Run 'helm plugin uninstall $(PLUGIN)' first, then 'make install'." >&2; \
		exit 1; \
	else \
		helm plugin install .; \
	fi

.PHONY: clean
clean:
	rm -rf bin .helm-mutation-test

.PHONY: help
help:
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "};{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
