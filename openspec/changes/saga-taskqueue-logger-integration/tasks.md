## 1. Saga runtime logging

- [ ] 1.1 Add an optional logger configuration to `saga.Config` and wire a module logger through `Engine`, `Coordinator`, `Poller`, and `Executor` without changing `NewEngine(cfg *Config)` signature
- [ ] 1.2 Replace runtime-path `fmt.Printf` / `log.Printf` calls in `saga` with repository `logger` calls and attach stable saga fields such as `tx_id`, `step_id`, `step_name`, and status where applicable
- [ ] 1.3 Rehydrate transaction-scoped trace context from persisted payload fields such as `_trace_id` / `_span_id` before coordinator/poller runtime logs are emitted in long-lived background paths
- [ ] 1.4 Use context-aware logger calls on transaction and step execution paths so trace context is automatically included when present or after rehydration
- [ ] 1.5 Add tests covering default global logger fallback, custom logger injection, representative saga runtime error or warning paths, and trace-context rehydration for background coordinator/poller logging

## 2. Taskqueue asynq logging integration

- [ ] 2.1 Add optional repository logger configuration to `taskqueue/asynqbroker` concrete implementations, including `Consumer` plus backward-compatible config-aware entry points for `Broker` and `Inspector`
- [ ] 2.2 Implement an asynq-to-repository-logger adapter so framework logs default to the repository logger when no explicit `asynq.Logger` is provided
- [ ] 2.3 Replace consumer wrapper `log.Printf` calls with repository logger calls and include task identifiers, queue metadata, and restored trace context when available
- [ ] 2.4 Audit `taskqueue/asynqbroker` runtime paths for any remaining direct standard-library logging, and route `Broker` / `Inspector` runtime logs through the same logger strategy

## 3. Validation and documentation

- [ ] 3.1 Add or update tests for logger integration behavior in `saga` and `taskqueue/asynqbroker`
- [ ] 3.2 Update `AGENTS.md`, `docs/saga.md`, `docs/modules/saga.md`, `saga/README.md`, `taskqueue/GUIDE.md`, and related `examples/` to reflect the new logger behavior and configuration
- [ ] 3.3 Run the relevant package test suites and verify there are no remaining runtime-path `fmt.Printf` / `log.Printf` calls in the scoped modules
