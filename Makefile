BINARY     := workbench
INSTALL    := /usr/local/bin/$(BINARY)
ZELLIJ_DIR := $(HOME)/.config/zellij/layouts

.PHONY: build install uninstall setup test fmt vet lint ci clean

build:
	go build -o $(BINARY) .

install: build
	cp $(BINARY) $(INSTALL)

uninstall:
	rm -f $(INSTALL)

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
