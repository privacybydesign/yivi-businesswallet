package idscan

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// The tasks a scan can fail at, as Failure names them.
const (
	TaskDocumentScan = taskDocumentScan
	TaskFaceLiveness = taskFaceLiveness
	TaskFaceMatch    = taskFaceMatch
)

// A transaction's states: running, done with a result, or ended without one.
const (
	StatePending   = "pending"
	StateCompleted = stateCompleted
	StateFailed    = "failed"
)

// A task's states once it ended without passing: the person stopped it, or
// it used up ID Scan's attempts.
const (
	StateAborted                 = "aborted"
	StateMaximumAttemptsExceeded = "maximum_attempts_exceeded"
)

// Status is a transaction's state, and on a failed one why it failed.
type Status struct {
	State   string
	Failure *Failure
}

// Failure is why a scan failed: the task that ended without passing, its
// state (StateAborted or StateMaximumAttemptsExceeded, empty when ID Scan
// gave none), how many attempts it took, and for the document scan its last
// attempt's error code (such as expiration_criteria_not_met).
type Failure struct {
	Task     string
	State    string
	Attempts int
	Code     string
}

type statusResponse struct {
	State string `json:"state"`
	Tasks []struct {
		Type          string `json:"type"`
		State         string `json:"state"`
		Attempts      int    `json:"attempts"`
		DocumentScans []struct {
			Code *string `json:"code"`
		} `json:"documentScans"`
	} `json:"tasks"`
}

// TransactionStatus reads transactionID's state, which needs no result id: a
// failed transaction has none. On a failed one it names the task that ended
// without passing; ID Scan runs the tasks in order, so that is the first one
// not completed.
func (c *Client) TransactionStatus(ctx context.Context, transactionID string) (Status, error) {
	var out statusResponse
	if err := c.call(ctx, http.MethodGet, c.baseURL+"/transactions/"+url.PathEscape(transactionID), nil, http.StatusOK, &out); err != nil {
		return Status{}, fmt.Errorf("idscan: status of transaction %s: %w", transactionID, err)
	}
	status := Status{State: out.State}
	if out.State != StateFailed {
		return status, nil
	}
	status.Failure = &Failure{}
	for _, task := range out.Tasks {
		if task.State == stateCompleted {
			continue
		}
		status.Failure = &Failure{Task: task.Type, State: task.State, Attempts: task.Attempts}
		if task.State == StatePending {
			status.Failure.State = ""
		}
		for _, scan := range task.DocumentScans {
			if scan.Code != nil {
				status.Failure.Code = *scan.Code
			}
		}
		break
	}
	return status, nil
}
