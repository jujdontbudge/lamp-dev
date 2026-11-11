PYTHON ?= python3
VENV_PYTHON = .venv/bin/python

.PHONY: sdk-pack build test test-all lint verify bootstrap generate check-generated check-contracts interop fmt

# Checked-in bindings mean ordinary builds do not need Python or protoc.
build:
	mkdir -p bin
	$(CC) -O2 -Wall -Wextra -Werror -o bin/lamp-plugin-launcher tools/plugin-launcher.c
	go build ./...
	$(PYTHON) tools/sdkgen.py --check
	cargo build --workspace --locked

# Selected smoke tests for ordinary verification.
test:
	go test -count=1 ./cmd/lamp -run '^TestCLIUsesCanonicalMutationHeaders$$'
	go test -count=1 ./internal/control -run '^(TestJournalReplaySnapshotBeforeLogTruncation|TestControlCoreInterfacesAndAuthority)$$'
	go test -count=1 ./pkg/plugin -run '^TestNativeSandboxDeniesAmbientAuthority$$'
	$(VENV_PYTHON) tests/contracts/test_schema.py Contracts.test_all_schemas_well_formed -v
	$(VENV_PYTHON) tests/ci/test_guard.py Guard.test_private_owner_push Guard.test_public_and_stranger_jobs_denied -v
ifeq ($(shell uname -s),Linux)
	cargo test --locked -p lamp-guard --lib killed_stopped_tracee_is_reaped_after_restart_esrch
	cargo test --locked -p lamp-init --bin lamp-guest-helper task_report_wins_even_when_collector_exit_arrives_first
endif

# The remaining automated suite; live VM and root guard smokes stay explicit.
test-all: test

bootstrap:
	$(PYTHON) -m venv .venv
	.venv/bin/pip install --disable-pip-version-check -r tools/requirements.txt

generate:
	$(PYTHON) tools/generate.py
	$(PYTHON) tools/protobuf.py
	$(PYTHON) tools/sdkgen.py

check-generated:
	$(PYTHON) tools/generate.py --check
	$(PYTHON) tools/protobuf.py --check
	$(PYTHON) tools/sdkgen.py --check

check-contracts:
	$(PYTHON) tools/check_contracts.py

interop:
	$(PYTHON) tools/interop.py

# CI builds and runs selected smokes, including cross-process Unix-socket/JSON.
verify: build test check-contracts interop
	$(PYTHON) tools/generate.py --check

lint:
	go vet ./...
	cargo fmt --all -- --check
	cargo clippy --workspace --all-targets --locked -- -D warnings

fmt:
	gofmt -w cmd internal pkg tools/contractprobe
	cargo fmt --all

# Release SDK packages (local only; T31 publishes).
sdk-pack:
	mkdir -p dist
	cd sdk/typescript && npm pack --pack-destination ../../dist
	$(PYTHON) -m pip wheel --no-deps -w dist sdk/python
