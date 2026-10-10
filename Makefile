.PHONY: build test check integration runtime-deps runtime-contracts dev-build web-deps web-build console-integration codex-interactions codex-recovery codex-lifecycle claude-deps claude-build claude-contracts claude-integration claude-recovery claude-idle
.PHONY: opencode-integration opencode-recovery opencode-contracts
.PHONY: console-listing console-observations console-alpha notification-contracts
.PHONY: codex-route-bindings
.PHONY: native-probe-contracts
.PHONY: codex-runtime-native
.PHONY: codex-application-native
.PHONY: codex-application-crash
.PHONY: execution-profiles
.PHONY: claude-auth-contracts
.PHONY: codex-skills-native

NETCAP_CODEX ?= codex
NETCAP_SERVER_CODEX ?= codex

build:
	mkdir -p bin
	go build -buildvcs=false -o bin/netcap ./cmd/netcap

test:
	go test -race ./...

check: test
	go vet ./...

integration: build
	python3 tests/go_cli_smoke.py --netcap "$(CURDIR)/bin/netcap" --codex "$(NETCAP_CODEX)" --server-codex "$(NETCAP_SERVER_CODEX)"

# Isolated development output; never replaces the user's running binary.
dev-build: web-build
	mkdir -p .dev/bin
	go build -buildvcs=false -o .dev/bin/netcap ./cmd/netcap

runtime-deps:
	npm --prefix tests/runtime ci --ignore-scripts --no-audit --no-fund

runtime-contracts:
	python3 tests/runtime/run.py

web-deps:
	npm --prefix web ci --ignore-scripts --no-audit --no-fund

web-build:
	npm --prefix web run build

console-integration: dev-build
	python3 tests/console_smoke.py --netcap "$(CURDIR)/.dev/bin/netcap"

codex-interactions: dev-build
	python3 tests/codex_interaction_smoke.py --netcap "$(CURDIR)/.dev/bin/netcap"

codex-recovery: dev-build
	python3 tests/codex_recovery_smoke.py --netcap "$(CURDIR)/.dev/bin/netcap"

codex-lifecycle: dev-build
	python3 tests/codex_lifecycle_smoke.py --netcap "$(CURDIR)/.dev/bin/netcap"

# Test-only stdio transport; the selected server must be the fixed native version.
codex-runtime-native:
	NETCAP_NATIVE_CODEX_TEST="$(NETCAP_SERVER_CODEX)" go test -tags=nativecodex -race ./internal/runtime -run '^TestCodexNativeRuntimePermissionAndRecovery$$' -count=1 -v

# Real SQLite/Application recovery with isolated native stdio and fault injection.
codex-application-native:
	NETCAP_NATIVE_CODEX_PROXY_TEST="$(NETCAP_CODEX)" NETCAP_NATIVE_CODEX_TEST="$(NETCAP_SERVER_CODEX)" go test -tags=nativecodex -race ./internal/runtime -run '^TestCodexNativeApplicationRecoversLostCreateReceipt$$' -count=1 -v

# Directory Skill discovery through Application/Handler with real native stdio.
codex-skills-native:
	NETCAP_NATIVE_CODEX_PROXY_TEST="$(NETCAP_CODEX)" NETCAP_NATIVE_CODEX_TEST="$(NETCAP_SERVER_CODEX)" go test -tags=nativecodex -race ./internal/runtime -run '^TestCodexNativeSkillDiscovery$$' -count=1 -v

# SIGKILL a fixture-owned Application process at durable commit boundaries.
codex-application-crash:
	NETCAP_NATIVE_CODEX_PROXY_TEST="$(NETCAP_CODEX)" NETCAP_NATIVE_CODEX_TEST="$(NETCAP_SERVER_CODEX)" go test -tags=nativecodex -race ./internal/runtime -run '^TestCodexNativeApplicationHardCrash$$' -count=1 -v

claude-deps:
	npm --prefix bridges/claude ci --ignore-scripts --no-audit --no-fund

claude-build:
	npm --prefix bridges/claude run build

claude-auth-contracts: claude-build
	node tests/claude_mcp_auth_test.mjs
	node tests/claude_mcp_helper_test.mjs
	node tests/claude_mcp_diagnostic_test.mjs
	node --experimental-test-module-mocks tests/claude_mcp_auth_recovery_test.mjs

claude-contracts: claude-auth-contracts
	python3 tests/claude_bridge_smoke.py
	python3 tests/claude_settings_smoke.py

claude-integration: claude-build dev-build
	python3 tests/claude_console_smoke.py --netcap "$(CURDIR)/.dev/bin/netcap"
	python3 tests/claude_interaction_smoke.py --netcap "$(CURDIR)/.dev/bin/netcap"

claude-recovery: claude-build dev-build
	python3 tests/claude_lifecycle_smoke.py --netcap "$(CURDIR)/.dev/bin/netcap"
	python3 tests/claude_recovery_smoke.py --netcap "$(CURDIR)/.dev/bin/netcap"

claude-idle: claude-build dev-build
	python3 tests/claude_idle_smoke.py --netcap "$(CURDIR)/.dev/bin/netcap"

opencode-integration: dev-build
	python3 tests/opencode_console_smoke.py --netcap "$(CURDIR)/.dev/bin/netcap"
	python3 tests/opencode_interaction_smoke.py --netcap "$(CURDIR)/.dev/bin/netcap"

opencode-recovery: dev-build
	python3 tests/opencode_recovery_smoke.py --netcap "$(CURDIR)/.dev/bin/netcap"
	python3 tests/opencode_lifecycle_smoke.py --netcap "$(CURDIR)/.dev/bin/netcap"
	python3 tests/opencode_pty_recovery_smoke.py --netcap "$(CURDIR)/.dev/bin/netcap"

opencode-contracts: dev-build
	python3 tests/opencode_settings_smoke.py --netcap "$(CURDIR)/.dev/bin/netcap"

console-listing: dev-build
	python3 tests/console_listing_smoke.py --netcap "$(CURDIR)/.dev/bin/netcap"

console-observations: dev-build
	python3 tests/console_observations_smoke.py --netcap "$(CURDIR)/.dev/bin/netcap"

console-alpha: claude-build dev-build
	python3 tests/console_alpha_smoke.py --netcap "$(CURDIR)/.dev/bin/netcap"

notification-contracts:
	go test -race -count=1 ./internal/events ./internal/notify ./internal/app

codex-route-bindings: dev-build
	python3 tests/codex_route_binding_smoke.py --netcap "$(CURDIR)/.dev/bin/netcap"

native-probe-contracts: claude-build
	mkdir -p .dev/bin
	go build -buildvcs=false -o .dev/bin/native-probe-fixture ./tests/nativeprobe
	python3 tests/native_probe_smoke.py --runner "$(CURDIR)/.dev/bin/native-probe-fixture" --failures

execution-profiles: claude-build dev-build
	mkdir -p .dev/w0201
	go build -buildvcs=false -o .dev/w0201/collision-fixture ./tests/executionprofiles
	python3 tests/claude_custom_settings_smoke.py
	python3 tests/execution_profiles_smoke.py --netcap "$(CURDIR)/.dev/bin/netcap"
	python3 tests/execution_profiles_collision.py
	python3 tests/execution_profiles_codex_collision.py
