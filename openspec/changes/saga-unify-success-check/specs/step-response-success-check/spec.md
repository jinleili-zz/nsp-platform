## ADDED Requirements

### Requirement: Unified step HTTP response success check function
The saga package SHALL export a public function `IsStepHTTPSuccess(statusCode int, body []byte) (map[string]any, bool, error)` that determines whether an HTTP response from a downstream subsystem indicates success. The function SHALL return `true` only when BOTH conditions are met:
1. HTTP status code is in the range `[200, 300)`
2. The response body is valid JSON and contains a top-level field `code` with value `"0"`

The `code` field SHALL be matched as string `"0"`. The function SHALL also accept numeric `0` (float64 or json.Number) as equivalent to `"0"`.

#### Scenario: HTTP 200 with code "0"
- **WHEN** statusCode is 200 and body is `{"code":"0","data":{"id":"123"}}`
- **THEN** the function SHALL return the parsed map, `true`, and `nil` error

#### Scenario: HTTP 200 with numeric code 0
- **WHEN** statusCode is 200 and body is `{"code":0,"data":{}}`
- **THEN** the function SHALL return the parsed map, `true`, and `nil` error

#### Scenario: HTTP 200 with code "1" (business failure)
- **WHEN** statusCode is 200 and body is `{"code":"1","message":"not found"}`
- **THEN** the function SHALL return the parsed map, `false`, and `nil` error

#### Scenario: HTTP 200 with empty body
- **WHEN** statusCode is 200 and body is empty
- **THEN** the function SHALL return `nil` map, `false`, and `nil` error

#### Scenario: HTTP 200 with non-JSON body
- **WHEN** statusCode is 200 and body is `not json`
- **THEN** the function SHALL return `nil` map, `false`, and a non-nil error describing the parse failure

#### Scenario: HTTP 200 with missing code field
- **WHEN** statusCode is 200 and body is `{"data":"ok"}`
- **THEN** the function SHALL return the parsed map, `false`, and `nil` error

#### Scenario: HTTP 500 with code "0"
- **WHEN** statusCode is 500 and body is `{"code":"0"}`
- **THEN** the function SHALL return `nil` map, `false`, and `nil` error (HTTP status takes precedence)

#### Scenario: HTTP 404
- **WHEN** statusCode is 404 and body is `{"code":"1","message":"not found"}`
- **THEN** the function SHALL return `nil` map, `false`, and `nil` error

### Requirement: ExecuteStep uses unified success check
`Executor.ExecuteStep` SHALL use `IsStepHTTPSuccess` to determine whether the synchronous step's HTTP response is successful, replacing the current `resp.StatusCode >= 200 && resp.StatusCode < 300` check.

#### Scenario: Sync step returns HTTP 200 but code "1"
- **WHEN** downstream returns HTTP 200 with body `{"code":"1","message":"failed"}`
- **THEN** ExecuteStep SHALL treat this as a failure and invoke `handleHTTPError`

#### Scenario: Sync step returns HTTP 200 with code "0"
- **WHEN** downstream returns HTTP 200 with body `{"code":"0","data":{}}`
- **THEN** ExecuteStep SHALL treat this as success, store the response, and mark step as succeeded

### Requirement: ExecuteAsyncStep uses unified success check
`Executor.ExecuteAsyncStep` SHALL use `IsStepHTTPSuccess` to determine whether the async step submission response is successful, replacing the current HTTP status-only check.

#### Scenario: Async step submission returns HTTP 200 but code "1"
- **WHEN** downstream returns HTTP 200 with body `{"code":"1","message":"rejected"}`
- **THEN** ExecuteAsyncStep SHALL treat this as a failure and invoke `handleHTTPError`

### Requirement: CompensateStep uses unified success check
`Executor.CompensateStep` SHALL use `IsStepHTTPSuccess` to determine whether the compensation response is successful, replacing the current HTTP status-only check.

#### Scenario: Compensation returns HTTP 200 but code "1"
- **WHEN** compensation endpoint returns HTTP 200 with body `{"code":"1","message":"failed"}`
- **THEN** CompensateStep SHALL treat this as a failed compensation attempt and retry

### Requirement: Poll uses unified success check
`Executor.Poll` SHALL use `IsStepHTTPSuccess` to determine whether the poll response is successful, replacing the current HTTP status-only check.

#### Scenario: Poll returns HTTP 200 but code "1"
- **WHEN** poll endpoint returns HTTP 200 with body `{"code":"1","message":"processing"}`
- **THEN** Poll SHALL return an error indicating the poll response was not successful
