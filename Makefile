BINARY     := workbench
ZELLIJ_DIR := $(HOME)/.config/zellij/layouts
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS    := -s -w -X github.com/panamafrancis/workbench/cmd.Version=$(VERSION)

.PHONY: build install setup test fmt vet lint ci clean hooks e2e release-patch release-minor release-major _do_release

build:
	CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o dist/workbench-linux-amd64  .
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o dist/workbench-darwin-arm64 .

install:
	go install -ldflags="$(LDFLAGS)" .

setup:
	mkdir -p $(ZELLIJ_DIR)
	cp scripts/wb.kdl $(ZELLIJ_DIR)/wb.kdl

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

lint:
	golangci-lint run ./...

ci: fmt lint vet test

hooks:
	git config core.hooksPath .githooks
	@echo "Git hooks enrolled from .githooks/"

e2e:
	go build -ldflags="$(LDFLAGS)" -o dist/workbench .
	PATH="$(CURDIR)/dist:$$PATH" bash scripts/e2e.sh

clean:
	rm -rf dist/

release-patch: BUMP = patch
release-minor: BUMP = minor
release-major: BUMP = major
release-patch release-minor release-major: _do_release

_do_release:
	@set -e; \
	git fetch origin main --tags; \
	latest_tag=$$(git tag -l 'v*' --sort=-v:refname | head -n 1); \
	if [ -z "$$latest_tag" ]; then \
		echo "no existing v* tags found"; \
		exit 1; \
	fi; \
	case "$(BUMP)" in \
		patch) new_tag=$$(echo "$$latest_tag" | awk -F. '{printf "%s.%s.%d", $$1, $$2, $$3+1}') ;; \
		minor) new_tag=$$(echo "$$latest_tag" | awk -F. '{printf "%s.%d.0", $$1, $$2+1}') ;; \
		major) new_tag=$$(echo "$$latest_tag" | awk -F. '{printf "v%d.0.0", substr($$1,2)+1}') ;; \
	esac; \
	echo "tagging $$new_tag on origin/main (from $$latest_tag)"; \
	git tag "$$new_tag" origin/main; \
	git push origin "$$new_tag"
