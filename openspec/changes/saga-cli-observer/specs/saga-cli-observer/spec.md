## ADDED Requirements

### Requirement: Observer SHALL list SAGA transactions with operational filters
The system SHALL provide a CLI command to list persisted SAGA transactions from PostgreSQL using read-only queries.
The list view MUST support filtering by persisted transaction status and MUST default to a bounded result set so operators can quickly locate active and failed transactions without issuing unbounded queries.

#### Scenario: List running transactions
- **WHEN** the operator runs the list command with a `running` status filter
- **THEN** the tool MUST display only transactions whose persisted status is `running`

#### Scenario: List compensating transactions
- **WHEN** the operator runs the list command with a `compensating` status filter
- **THEN** the tool MUST display only transactions whose persisted status is `compensating`

#### Scenario: List pending transactions
- **WHEN** the operator runs the list command with a `pending` status filter
- **THEN** the tool MUST display only transactions whose persisted status is `pending`

#### Scenario: List command uses a bounded default limit
- **WHEN** the operator runs the list command without an explicit limit
- **THEN** the tool MUST return only the most recent 100 results by default and MUST provide a way to override that limit explicitly

#### Scenario: List command indicates truncation
- **WHEN** matching rows exceed the effective result limit
- **THEN** the tool MUST indicate that the output has been truncated by the current limit

#### Scenario: List recent failed transactions
- **WHEN** the operator runs the failed shortcut command
- **THEN** the tool MUST display transactions whose persisted status is `failed`, ordered by `finished_at` descending and falling back to `updated_at` descending when `finished_at` is unavailable

#### Scenario: Failed command uses a bounded default limit
- **WHEN** the operator runs the failed command without an explicit limit
- **THEN** the tool MUST return only the most recent 100 failed transactions by default and MUST provide a way to override that limit explicitly

### Requirement: Observer SHALL show transaction detail with ordered step state
The system SHALL provide a detail command that displays a single transaction and its steps using data from `saga_transactions`, `saga_steps`, and `saga_poll_tasks`.
The detail view MUST show transaction-level status and step-level execution state in step index order.

#### Scenario: Show synchronous transaction detail
- **WHEN** the operator requests detail for an existing transaction containing synchronous steps
- **THEN** the tool MUST display the transaction summary and each step's `status`, `retry_count`, `started_at`, `finished_at`, and `last_error`

#### Scenario: Show asynchronous transaction detail
- **WHEN** the operator requests detail for an existing transaction containing an asynchronous polling step
- **THEN** the tool MUST display the step's `poll_count`, `poll_max_times`, and the current poll task timing information when present

#### Scenario: Show transaction lock ownership
- **WHEN** the operator requests detail for a transaction that is currently locked by an engine instance
- **THEN** the transaction summary MUST display `locked_by` and `locked_until` when those fields are present

### Requirement: Observer SHALL provide an auto-refresh watch mode
The system SHALL provide a terminal watch mode for a single transaction.
Watch mode MUST periodically refresh the transaction snapshot without requiring the operator to rerun the command manually.

#### Scenario: Watch a polling transaction
- **WHEN** the operator runs watch mode for a transaction whose current step is in `polling`
- **THEN** the tool MUST refresh the displayed transaction and step state on a fixed interval and show updated poll counts or terminal status when they change

#### Scenario: Watch a transaction through compensation
- **WHEN** the observed transaction transitions from `running` to `compensating` and then to `failed`
- **THEN** the tool MUST update the watch view to reflect the new persisted transaction and step states on subsequent refreshes

### Requirement: Observer SHALL remain read-only
The observer tool MUST NOT mutate persisted SAGA state.
It MUST NOT submit transactions, update step state, release or acquire execution locks, trigger compensation, or retry failed work.

#### Scenario: Querying a transaction does not change state
- **WHEN** the operator runs list, show, failed, or watch commands
- **THEN** the tool MUST only execute read-only database queries and MUST NOT change any row in `saga_transactions`, `saga_steps`, or `saga_poll_tasks`

### Requirement: Observer SHALL tolerate partial and legacy records
The observer tool SHALL handle existing SAGA rows that do not contain optional or derived fields such as transaction name or trace metadata.
Missing optional data MUST degrade gracefully instead of causing the command to fail.

#### Scenario: Transaction without trace metadata
- **WHEN** the selected transaction payload does not contain `_trace_id`
- **THEN** the tool MUST still render the transaction detail successfully and display trace metadata as unavailable

#### Scenario: Async step without active poll task
- **WHEN** an async step is in a terminal state and no row exists in `saga_poll_tasks` for that step
- **THEN** the tool MUST render the step detail successfully without treating the missing poll task as an error
