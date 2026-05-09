APP_NAME = osqueryi-mcp

.PHONY: all build run test fmt vet install clean

all: fmt vet build

build:
	go build -o $(APP_NAME) ./cmd/$(APP_NAME)

run:
	go run ./cmd/$(APP_NAME)

test:
	go test -v ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

install: build
	cp $(APP_NAME) ~/.local/bin/

clean:
	rm -f $(APP_NAME)
