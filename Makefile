BINARY     := workbench
ZELLIJ_DIR := $(HOME)/.config/zellij/layouts

.PHONY: build install setup test fmt vet lint ci clean

build:
	go build -o $(BINARY) .

install: build
	go install .

setup:
	mkdir -p $(ZELLIJ_DIR)
	cp scripts/wb.kdl $(ZELLIJ_DIR)/wb.kdl

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

ci: fmt vet test build

clean:
	rm -f $(BINARY)
