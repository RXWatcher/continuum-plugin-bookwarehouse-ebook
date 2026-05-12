BINARY := continuum-plugin-bookwarehouse-ebook
GO ?= go

.PHONY: build test clean
build:
	$(GO) build -o $(BINARY) ./cmd/continuum-plugin-bookwarehouse-ebook
test:
	$(GO) test ./...
clean:
	rm -f $(BINARY)
