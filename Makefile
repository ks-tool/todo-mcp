.PHONY: build
build:
	@CGO_ENABLED=0 go build -ldflags='-s -w' -trimpath -o todo ./cmd/todo
