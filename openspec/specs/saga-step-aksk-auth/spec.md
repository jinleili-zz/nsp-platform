# saga-step-aksk-auth Specification

## Purpose
TBD - created by archiving change saga-subtx-aksk-auth. Update Purpose after archive.
## Requirements
### Requirement: Step supports AK authentication configuration
A `Step` SHALL support optional AK credential via `AuthAK` string field. When `AuthAK` is non-empty, the Executor SHALL look up the full credential (including SK) from `auth.CredentialStore` and sign the outbound HTTP request using NSP-HMAC-SHA256 before sending. When `AuthAK` is empty, the Executor SHALL send the request without authentication headers (backward-compatible unsigned behavior).

#### Scenario: Step with AuthAK sends signed request
- **WHEN** a Step has non-empty `AuthAK` and the corresponding credential exists in CredentialStore
- **THEN** the outbound HTTP request SHALL contain `Authorization`, `X-NSP-Timestamp`, `X-NSP-Nonce`, and `X-NSP-SignedHeaders` headers per the NSP-HMAC-SHA256 scheme

#### Scenario: Step with empty AuthAK sends unsigned request
- **WHEN** a Step has empty `AuthAK`
- **THEN** the outbound HTTP request SHALL NOT contain `Authorization`, `X-NSP-Timestamp`, `X-NSP-Nonce`, or `X-NSP-SignedHeaders` headers

### Requirement: AK is persisted through the store lifecycle, SK is never stored in DB
`AuthAK` SHALL be written to the `saga_steps` table on step creation and read back on every `GetSteps`/`GetStep` call. SK SHALL NOT be stored in the database; it SHALL be resolved at execution time via `auth.CredentialStore.GetByAK(ak)`.

#### Scenario: AuthAK survives a round-trip through the store
- **WHEN** a Step with non-empty `AuthAK` is created via `CreateSteps` or `CreateTransactionWithSteps`
- **THEN** a subsequent `GetSteps` or `GetStep` call SHALL return a Step with the same non-empty `AuthAK` value

#### Scenario: Worker-loaded steps resolve credentials at execution time
- **WHEN** a coordinator or poller loads a Step from the database using `GetStep`
- **THEN** the returned Step SHALL have `AuthAK` populated if it was set at creation, and the Executor SHALL use `CredentialStore.GetByAK` to resolve the SK for signing

### Requirement: AK/SK signing applies to all HTTP call types
The Executor SHALL apply AK/SK signing consistently to all three HTTP call types: forward action, compensation action, and poll request.

#### Scenario: Forward action request is signed
- **WHEN** `ExecuteStep` or `ExecuteAsyncStep` is called with a Step that has non-empty AuthAK
- **THEN** the action HTTP request SHALL be signed before sending

#### Scenario: Compensation action request is signed
- **WHEN** `CompensateStep` is called with a Step that has non-empty AuthAK
- **THEN** the compensation HTTP request SHALL be signed before sending

#### Scenario: Poll request is signed
- **WHEN** `Poll` is called with a Step that has non-empty AuthAK
- **THEN** the poll HTTP request SHALL be signed before sending
- **NOTE** Poll requests typically have no body (nil); the signer SHALL handle nil/empty body correctly by hashing empty content

### Requirement: Signing failure results in fatal step error
If signing fails (e.g., credential not found, credential disabled, body too large, nonce generation error), the Executor SHALL treat the failure as a fatal error and SHALL NOT retry the step.

#### Scenario: Sign error on forward action is fatal
- **WHEN** signing fails during forward action execution (credential lookup failure or `auth.Signer.Sign` error)
- **THEN** the step SHALL transition to `StepStatusFailed` and return `ErrStepFatal` without incrementing the retry counter

#### Scenario: Sign error on compensation is fatal
- **WHEN** signing fails during compensation execution
- **THEN** the compensation SHALL return `ErrCompensationFailed` immediately

#### Scenario: Sign error on poll is fatal
- **WHEN** signing fails during poll execution
- **THEN** the poller SHALL treat it as a terminal failure: mark the step as `StepStatusFailed`, delete the poll task, and notify the coordinator to trigger compensation; the poller SHALL NOT release the poll task for retry

### Requirement: Submit-time fail-fast validation
When `CredentialStore` is available, `Engine.Submit()` SHALL validate that every Step with non-empty `AuthAK` has a corresponding enabled credential in the CredentialStore. If validation fails, `Submit()` SHALL return an error and SHALL NOT create the transaction.

#### Scenario: Submit with unknown AuthAK is rejected
- **WHEN** a Step has `AuthAK = "unknown-ak"` and no matching credential exists in CredentialStore
- **THEN** `Engine.Submit()` SHALL return an error and SHALL NOT persist the transaction

#### Scenario: Submit without CredentialStore skips validation
- **WHEN** `Engine.Config.CredentialStore` is nil
- **THEN** `Engine.Submit()` SHALL NOT perform AK validation (but Executor will also not sign any requests)

### Requirement: Existing steps remain backward compatible
Adding `AuthAK` field to the `Step` struct and `auth_ak` column to `saga_steps` (with `DEFAULT ''`) SHALL be purely additive. All existing Step definitions and stored rows with zero-value auth field SHALL behave identically to pre-change behavior.

#### Scenario: Zero-value auth field causes no behavioral change
- **WHEN** a Step is constructed without setting `AuthAK`
- **THEN** the Executor behavior SHALL be identical to pre-change behavior with no additional headers

#### Scenario: Existing DB rows with empty auth column are not signed
- **WHEN** a step row in `saga_steps` has `auth_ak = ''` (existing data after migration)
- **THEN** the Executor SHALL send requests for that step without authentication headers

