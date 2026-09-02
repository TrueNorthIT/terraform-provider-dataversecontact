default: testacc

# Run acceptance tests
.PHONY: testacc
testacc:
	TF_ACC=1 go test ./... -v $(TESTARGS) -timeout 120m

# Run unit tests
.PHONY: test
test:
	go test ./... -v $(TESTARGS) -timeout 30m

# Build provider
.PHONY: build
build:
	go build -o terraform-provider-dataversecontact

# Install provider locally for development
.PHONY: install
install: build
	mkdir -p ~/.terraform.d/plugins/registry.terraform.io/TrueNorthIT/dataversecontact/0.0.1/$$(go env GOOS)_$$(go env GOARCH)
	cp terraform-provider-dataversecontact ~/.terraform.d/plugins/registry.terraform.io/TrueNorthIT/dataversecontact/0.0.1/$$(go env GOOS)_$$(go env GOARCH)/

# Regenerate registry documentation (docs/) from schemas, templates/ and examples/
.PHONY: docs
docs:
	go generate ./...

# Fail if committed docs/ or examples/ formatting are stale
.PHONY: docs-check
docs-check: docs
	@git diff --exit-code -- docs/ examples/ || (echo "docs out of date: run 'make docs' and commit"; exit 1)

# terraform-validate every example against the current provider schema
.PHONY: validate-examples
validate-examples:
	./scripts/validate-examples.sh

# Print the CLI config stanza for local provider development. Modern
# terraform resolves a local build via dev_overrides, not the legacy
# filesystem mirror the `install` target populates.
.PHONY: dev-override
dev-override: build
	@echo 'Add to ~/.terraformrc (or %APPDATA%\terraform.rc), then skip terraform init:'
	@echo 'provider_installation {'
	@echo '  dev_overrides {'
	@echo '    "TrueNorthIT/dataversecontact" = "$(CURDIR)"'
	@echo '  }'
	@echo '  direct {}'
	@echo '}'

# Lint
.PHONY: lint
lint:
	golangci-lint run ./...

# Format
.PHONY: fmt
fmt:
	gofmt -s -w .
