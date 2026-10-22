HOOKS := load-guard heavy-run-guard
TARGETS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64

.PHONY: build test dist clean
build:
	@for h in $(HOOKS); do go build -o bin/$$h ./cmd/$$h; done

test:
	go vet ./... && go test ./...

dist:
	@for t in $(TARGETS); do for h in $(HOOKS); do \
		GOOS=$${t%/*} GOARCH=$${t#*/} go build -o dist/$$h-$${t%/*}-$${t#*/} ./cmd/$$h; \
	done; done

clean:
	rm -rf bin dist
