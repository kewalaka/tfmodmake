.PHONY: build
build:
	go build ./cmd/tfmodmake

.PHONY: install
install:
	go install ./cmd/tfmodmake

.PHONY: test
test:
	go test -count=1 ./...
