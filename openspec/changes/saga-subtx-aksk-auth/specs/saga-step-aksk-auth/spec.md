## ADDED Requirements

### Requirement: Step supports AK/SK authentication configuration
A `Step` SHALL support optional AK/SK credentials via `AuthAK` and `AuthSK` string fields. When both fields are non-empty, the Executor SHALL sign the outbound HTTP request using NSP-HMAC-SHA256 before sending. When both fields are empty, the Executor SHALL send the request without authentication headers (backward-compatible unsigned behavior).

#### Scenario: Step with both AK and SK sends signed request
- **WHEN** a Step has non-empty `AuthAK` and non-empty `AuthSK`
- **THEN** the outbound HTTP request SHALL contain `Authorization`, `X-NSP-Timestamp`, `X-NSP-Nonce`, and `X-NSP-SignedHeaders` headers per the NSP-HMAC-SHA256 scheme

#### Scenario: Step with both fields empty sends unsigned request
- **WHEN** a Step has empty `AuthAK` and empty `AuthSK`
- **THEN** the outbound HTTP request SHALL NOT contain `Authorization`, `X-NSP-Timestamp`, `X-NSP-Nonce`, or `X-NSP-SignedHeaders` headers

### Requirement: Partial AK/SK configuration is rejected at build time
`SagaBuilder.Build()` SHALL reject a Step where exactly one of `AuthAK` or `AuthSK` is non-empty, returning `ErrStepPartialAuth`. This prevents silent authentication disablement caused by a misconfigured credential injection.

#### Scenario: Only AK set is rejected
- **WHEN** a Step has non-empty `AuthAK` and empty `AuthSK`
- **THEN** `SagaBuilder.Build()` SHALL return `ErrStepPartialAuth` and SHALL NOT create the transaction

#### Scenario: Only SK set is rejected
- **WHEN** a Step has empty `AuthAK` and non-empty `AuthSK`
- **THEN** `SagaBuilder.Build()` SHALL return `ErrStepPartialAuth` and SHALL NOT create the transaction

### Requirement: AK/SK credentials are persisted through the store lifecycle
`AuthAK` and `AuthSK` SHALL be written to the `saga_steps` table on step creation and read back on every `GetSteps`/`GetStep` call, so that coordinator and poller workers always have access to the credentials when driving execution from the database.

#### Scenario: Credentials survive a round-trip through the store
- **WHEN** a Step with non-empty `AuthAK` and `AuthSK` is created via `CreateSteps` or `CreateTransactionWithSteps`
- **THEN** a subsequent `GetSteps` or `GetStep` call SHALL return a Step with the same non-empty `AuthAK` and `AuthSK` values

#### Scenario: Worker-loaded steps carry credentials for signing
- **WHEN** a coordinator or poller loads a Step from the database using `GetStep`
- **THEN** the returned Step SHALL have `AuthAK` and `AuthSK` populated if they were set at creation, and the Executor SHALL sign requests for that step

### Requirement: AK/SK signing applies to all HTTP call types
The Executor SHALL apply AK/SK signing consistently to all three HTTP call types: forward action, compensation action, and poll request.

#### Scenario: Forward action request is signed
- **WHEN** `ExecuteStep` or `ExecuteAsyncStep` is called with a Step that has non-empty AK/SK
- **THEN** the action HTTP request SHALL be signed before sending

#### Scenario: Compensation action request is signed
- **WHEN** `CompensateStep` is called with a Step that has non-empty AK/SK
- **THEN** the compensation HTTP request SHALL be signed before sending

#### Scenario: Poll request is signed
- **WHEN** `Poll` is called with a Step that has non-empty AK/SK
- **THEN** the poll HTTP request SHALL be signed before sending

### Requirement: Signing failure results in fatal step error
If signing fails (e.g., body too large, nonce generation error), the Executor SHALL treat the failure as a fatal error and SHALL NOT retry the step.

#### Scenario: Sign error on forward action is fatal
- **WHEN** `auth.Signer.Sign` returns an error during forward action execution
- **THEN** the step SHALL transition to `StepStatusFailed` and return `ErrStepFatal` without incrementing the retry counter

#### Scenario: Sign error on compensation is fatal
- **WHEN** `auth.Signer.Sign` returns an error during compensation execution
- **THEN** the compensation SHALL return `ErrCompensationFailed` immediately

### Requirement: Existing steps remain backward compatible
Adding `AuthAK`/`AuthSK` fields to the `Step` struct and `auth_ak`/`auth_sk` columns to `saga_steps` (with `DEFAULT ''`) SHALL be purely additive. All existing Step definitions and stored rows with zero-value auth fields SHALL behave identically to pre-change behavior.

#### Scenario: Zero-value auth fields cause no behavioral change
- **WHEN** a Step is constructed without setting `AuthAK` or `AuthSK`
- **THEN** the Executor behavior SHALL be identical to pre-change behavior with no additional headers

#### Scenario: Existing DB rows with empty auth columns are not signed
- **WHEN** a step row in `saga_steps` has `auth_ak = ''` and `auth_sk = ''` (existing data after migration)
- **THEN** the Executor SHALL send requests for that step without authentication headers
