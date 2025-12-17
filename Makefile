.PHONY: build
build:
	go build ./cmd/tfmodmake

.PHONY: install
install:
	go install ./cmd/tfmodmake
