package desktop

// AskResult is the answer to a yes/no/cancel question.
type AskResult int

const (
	AskCancel AskResult = iota
	AskYes
	AskNo
)
