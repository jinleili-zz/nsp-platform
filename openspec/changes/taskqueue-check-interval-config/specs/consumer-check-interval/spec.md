## ADDED Requirements

### Requirement: ConsumerConfig exposes TaskCheckInterval field

`asynqbroker.ConsumerConfig` SHALL include a `TaskCheckInterval time.Duration` field, allowing users to configure the interval between asynq server checks for new tasks in empty queues.

#### Scenario: User sets TaskCheckInterval to a valid value
- **WHEN** `ConsumerConfig.TaskCheckInterval` is set to `500ms`
- **THEN** the created asynq server MUST use `500ms` as its `TaskCheckInterval`

#### Scenario: User does not set TaskCheckInterval (zero value)
- **WHEN** `ConsumerConfig.TaskCheckInterval` is `0` (zero value)
- **THEN** the asynq server MUST use its internal default (`1s`), the field SHALL NOT be set on `asynq.Config`

### Requirement: TaskCheckInterval minimum boundary enforcement

The system SHALL enforce a minimum value of `200ms` for `TaskCheckInterval`.

#### Scenario: Value below minimum is clamped
- **WHEN** `ConsumerConfig.TaskCheckInterval` is set to `100ms`
- **THEN** the effective value passed to asynq MUST be `200ms`

#### Scenario: Value at exactly minimum is accepted
- **WHEN** `ConsumerConfig.TaskCheckInterval` is set to `200ms`
- **THEN** the effective value passed to asynq MUST be `200ms`

### Requirement: TaskCheckInterval maximum boundary enforcement

The system SHALL enforce a maximum value of `2s` for `TaskCheckInterval`.

#### Scenario: Value above maximum is clamped
- **WHEN** `ConsumerConfig.TaskCheckInterval` is set to `5s`
- **THEN** the effective value passed to asynq MUST be `2s`

#### Scenario: Value at exactly maximum is accepted
- **WHEN** `ConsumerConfig.TaskCheckInterval` is set to `2s`
- **THEN** the effective value passed to asynq MUST be `2s`

### Requirement: Negative TaskCheckInterval treated as zero

The system SHALL treat negative `TaskCheckInterval` values the same as zero (use asynq default).

#### Scenario: Negative value falls back to default
- **WHEN** `ConsumerConfig.TaskCheckInterval` is set to `-1s`
- **THEN** the asynq server MUST use its internal default (`1s`)

### Requirement: Boundary constants are exported

The package SHALL export `MinTaskCheckInterval` and `MaxTaskCheckInterval` constants so that callers can reference the valid range programmatically.

#### Scenario: Constants are accessible
- **WHEN** a caller imports `asynqbroker`
- **THEN** `asynqbroker.MinTaskCheckInterval` MUST equal `200ms` and `asynqbroker.MaxTaskCheckInterval` MUST equal `2s`

### Requirement: Clamped value triggers warning log

When the provided `TaskCheckInterval` is clamped (below min or above max), the system SHALL emit a warning-level log message indicating the original value and the clamped result.

#### Scenario: Below-minimum value logs warning
- **WHEN** `ConsumerConfig.TaskCheckInterval` is set to `50ms`
- **THEN** a warning log MUST be emitted containing the original value `50ms` and the clamped value `200ms`

#### Scenario: Above-maximum value logs warning
- **WHEN** `ConsumerConfig.TaskCheckInterval` is set to `10s`
- **THEN** a warning log MUST be emitted containing the original value `10s` and the clamped value `2s`
