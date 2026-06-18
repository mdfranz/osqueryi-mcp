RUST_BIN   = osquery-rs-mcp
GO_BIN     = osquery-go-mcp
GIT_COMMIT := $(shell git rev-parse --short HEAD)

.PHONY: all build build-rust build-go \
        run run-rust run-go \
        test test-rust test-go \
        fmt fmt-rust fmt-go \
        vet vet-rust vet-go \
        install clean clean-rust clean-go

all: fmt build

# --- build ---
build: build-rust build-go

build-rust:
	GIT_COMMIT=$(GIT_COMMIT) cargo build --manifest-path rust/Cargo.toml --release
	cp rust/target/release/$(RUST_BIN) $(RUST_BIN)

build-go:
	cd golang && go build -ldflags "-X main.GitCommit=$(GIT_COMMIT)" -o ../$(GO_BIN) ./cmd/osqueryi-mcp

# --- run ---
run: run-rust

run-rust:
	GIT_COMMIT=$(GIT_COMMIT) cargo run --manifest-path rust/Cargo.toml

run-go:
	./$(GO_BIN)

# --- test ---
test: test-rust test-go

test-rust:
	cargo test --manifest-path rust/Cargo.toml

test-go:
	cd golang && go test ./...

# --- fmt ---
fmt: fmt-rust fmt-go

fmt-rust:
	cargo fmt --manifest-path rust/Cargo.toml

fmt-go:
	cd golang && gofmt -w .

# --- vet ---
vet: vet-rust vet-go

vet-rust:
	cargo clippy --manifest-path rust/Cargo.toml

vet-go:
	cd golang && go vet ./...

# --- install ---
install: build
	cp $(RUST_BIN) ~/.local/bin/
	cp $(GO_BIN) ~/.local/bin/

# --- clean ---
clean: clean-rust clean-go

clean-rust:
	cargo clean --manifest-path rust/Cargo.toml
	rm -f $(RUST_BIN)

clean-go:
	cd golang && go clean ./...
	rm -f $(GO_BIN)
