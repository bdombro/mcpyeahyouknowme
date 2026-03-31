# Testing Guidelines for Contributors

## Quick Reference

- **Run tests**: `./scripts/test.sh`
- **View HTML coverage**: Open `coverage/coverage.html` in a browser
- **Test MCP integration**: `./scripts/test-mcp.sh`
- **Current coverage**: **100%** of core business logic (filtered)

## Documentation

For detailed information about our testing strategy, see [docs/test-spec.md](docs/test-spec.md).

## Critical Rule for Test Changes

⚠️ **IMPORTANT**: Whenever you make changes to the testing infrastructure, **you MUST update [docs/test-spec.md](docs/test-spec.md)** to reflect:

- Changes to coverage filtering (which files/lines are included/excluded)
- New testing tools or patterns
- Changes to what's considered "core business logic" vs "infrastructure"

The test-spec.md document is the **single source of truth** for understanding our testing philosophy and must stay aligned with the actual test implementation.

## Making Test Changes

### Adding Tests

When adding new test files:
1. Place them in `src/` with a `_test.go` suffix
2. Use table-driven tests where appropriate
3. Mock external dependencies (filesystem, network, etc.)
4. Add coverage assertions for critical paths

### Modifying Coverage Filtering

If you need to change which files or lines are included in coverage metrics:

1. **Update `scripts/test.sh`** — Modify the `grep` patterns in the script
2. **Update `docs/test-spec.md`** — Document the change and rationale
3. **Run tests** — Verify coverage remains at 100%
4. **Commit both changes together** — Test code and docs in sync

### When Line Numbers Shift

After editing source files, coverage may drop below 100% because the line-range exclusions in `scripts/test.sh` no longer match. To fix:

1. Run `./scripts/test.sh` — check if total dropped
2. Open `coverage/coverage_filtered.txt` — find new uncovered lines  
3. Update the `grep -v` patterns in `scripts/test.sh` with corrected line numbers
4. Run `./scripts/test.sh` again to verify 100%

## Test File Organization

```
src/
├── *_test.go              # Unit tests (same package)
├── testutil_test.go       # Shared test utilities (newTestStore, seedFixtures)
├── coverage.out           # Full coverage data (all files)
└── coverage_filtered.out  # Filtered coverage (core logic only)
```

## Coverage Target

- **Core business logic**: **100%** (filtered — DB errors, ONNX panics, constructors excluded)
- **Infrastructure/CLI**: No minimum (excluded from metrics)

## Testing Philosophy

We follow a **pragmatic testing approach**:

✅ **Comprehensive unit tests** for:
- Algorithms (search, fuzzy matching, RRF fusion)
- Data processing (message formatting, embedding)
- Business logic (chat filtering, contact search, name resolution)

⚠️ **Excluded from coverage** (legitimate reasons):
- DB error paths (require mocking `*sql.DB`)
- ONNX panic/error recovery (require real ONNX failures)
- Constructor init errors (filesystem/SQLite init)
- OS-dependent paths (architecture-specific)

❌ **Manual/integration testing only** for:
- CLI command routing, OS integration (LaunchAgents)
- OAuth flows, WhatsApp event loops

See [docs/test-spec.md](docs/test-spec.md) for detailed rationale.

## History

- **March 2026**: Implemented filtered coverage (87.5% core logic)
- **March 2026**: Added main_test.go, daemon_test.go
- **March 2026**: Moved install-daemon from Go to shell script
