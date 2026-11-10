PYTHON ?= python3
VENV_PYTHON = .venv/bin/python

.PHONY: build test verify bootstrap generate check-generated check-contracts interop fmt

# Checked-in bindings mean ordinary builds do not need Python or protoc.
build:
	go build ./...
	cargo build --workspace --locked

test:
	go test ./...
	cargo test --workspace --locked
	$(PYTHON) -m unittest discover -s tests/benchmarks -v

bootstrap:
	$(PYTHON) -m venv .venv
	.venv/bin/pip install --disable-pip-version-check -r tools/requirements.txt

generate:
	$(PYTHON) tools/generate.py
	$(PYTHON) tools/protobuf.py

check-generated:
	$(PYTHON) tools/generate.py --check
	$(PYTHON) tools/protobuf.py --check

check-contracts:
	$(PYTHON) tools/check_contracts.py
	$(VENV_PYTHON) -m unittest discover -s tests/contracts -v
	$(VENV_PYTHON) -m unittest discover -s tests/ci -v

interop:
	$(PYTHON) tools/interop.py

# Includes real cross-process Unix-socket/JSON checks, not an engine benchmark.
verify: build test check-contracts interop
	$(PYTHON) tools/generate.py --check
	go vet ./...
	cargo fmt --all -- --check
	cargo clippy --workspace --all-targets --locked -- -D warnings

fmt:
	gofmt -w cmd internal pkg tools/contractprobe
	cargo fmt --all
