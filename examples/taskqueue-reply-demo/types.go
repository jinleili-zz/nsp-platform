package replydemo

// Queue names follow the service:action:direction convention.
const (
	CalcRequestQueue  = "calc:request:incoming"
	CalcResponseQueue = "calc:response:outgoing"
)

// Task type constants.
const (
	TaskTypeCalc      = "calc:task"
	TaskTypeCalcReply = "calc:reply"
)

// CalcTask represents a calculation request sent from Producer to Consumer.
type CalcTask struct {
	TaskID    string    `json:"task_id"`
	Operation string    `json:"operation"`
	Operands  []float64 `json:"operands"`
}

// CalcResult represents a calculation reply sent from Consumer back to Producer.
type CalcResult struct {
	TaskID string  `json:"task_id"`
	Result float64 `json:"result"`
	Error  string  `json:"error,omitempty"`
}
