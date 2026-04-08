## 1. Observer command scaffolding

- [ ] 1.1 Create a new observer command entrypoint for the SAGA module and define subcommands for `list`, `show`, `watch`, and `failed`
- [ ] 1.2 Add configuration loading for a read-only PostgreSQL DSN via flags and/or environment variables
- [ ] 1.3 Decide and wire the TUI dependency or lightweight terminal refresh implementation for watch mode

## 2. Read model and query layer

- [ ] 2.1 Add a dedicated read-only query package for loading transaction summaries, transaction detail, ordered steps, and poll task state from existing `saga_*` tables
- [ ] 2.2 Define observer-facing view models that include status, timing, retry, polling, trace, and error summary fields
- [ ] 2.3 Implement list and failed queries with status filtering, sorting, and bounded result limits suitable for operator use
- [ ] 2.4 Implement single-transaction detail queries that join transaction, steps, and poll task data without taking execution locks

## 3. CLI and TUI presentation

- [ ] 3.1 Implement plain CLI output for `list` and `failed`
- [ ] 3.2 Implement plain CLI output for `show <tx_id>`, including transaction summary and ordered steps
- [ ] 3.3 Implement `watch <tx_id>` with interval-based auto refresh and a transaction detail screen suitable for interactive observation
- [ ] 3.4 Add concise formatting and truncation rules for large JSON payload or response fields so the terminal output remains readable

## 4. Validation and documentation

- [ ] 4.1 Add tests for the read-only query layer covering running, failed, polling, compensated, and missing poll task scenarios
- [ ] 4.2 Add command-level tests or golden outputs for `list`, `show`, and `failed`
- [ ] 4.3 Update `docs/saga.md`, `docs/modules/saga.md`, and `saga/README.md` with observer tool usage and first-phase scope limits
- [ ] 4.4 Run the relevant test suite and verify the observer commands against a local test database snapshot
