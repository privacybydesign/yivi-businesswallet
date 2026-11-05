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
	// Running is, on a pending transaction, the task the person is at: the
	// first one not completed. nil when none is.
	Running *Running
}

// Running is the task a pending transaction is at, and its attempts so far.
type Running struct {
	Task     string
	Attempts int
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
// not completed. A pending one whose liveness check used the tries the photo
// left is failed too (spentLiveness).
func (c *Client) TransactionStatus(ctx context.Context, transactionID string) (Status, error) {
	var out statusResponse
	if err := c.call(ctx, http.MethodGet, c.baseURL+"/transactions/"+url.PathEscape(transactionID), nil, http.StatusOK, &out); err != nil {
		return Status{}, fmt.Errorf("idscan: status of transaction %s: %w", transactionID, err)
	}
	if failure := spentLiveness(out); failure != nil {
		return Status{State: StateFailed, Failure: failure}, nil
	}
	status := Status{State: out.State}
	if out.State == StatePending {
		status.Running = runningTask(out)
	}
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

// runningTask is the first task of out not completed. ID Scan leaves a
// pending face_match out of its answer, so a completed liveness check with
// nothing after it means the match is running.
func runningTask(out statusResponse) *Running {
	livenessDone := false
	for _, task := range out.Tasks {
		if task.State != stateCompleted {
			return &Running{Task: task.Type, Attempts: task.Attempts}
		}
		livenessDone = livenessDone || task.Type == taskFaceLiveness
	}
	if livenessDone {
		return &Running{Task: taskFaceMatch}
	}
	return nil
}

// attemptsLimit is ID Scan's number of tries in a transaction (its
// maximumAttemptsDocumentScan). Its page hands the liveness check the tries
// the photo left, one used per failed photo, and once those are used shows an
// error page while its API leaves the liveness check pending.
const attemptsLimit = 10

// codeBackSideRequired is a document scan's answer to a good front of a
// two-sided document: no failed photo, the page asks for the back.
const codeBackSideRequired = "front_side_provided_back_side_required"

// spentLiveness is the liveness check of a pending transaction once it used
// the tries the photo left; nil before. ID Scan counts every liveness try that
// started, also one its page did not count (an error), so this can end a try
// early but never leaves the person on that error page.
func spentLiveness(out statusResponse) *Failure {
	if out.State != StatePending {
		return nil
	}
	photoDone, failedPhotos := false, 0
	for _, task := range out.Tasks {
		switch {
		case task.Type == taskDocumentScan && task.State == stateCompleted:
			photoDone = true
			for _, scan := range task.DocumentScans {
				if scan.Code != nil && *scan.Code != codeBackSideRequired {
					failedPhotos++
				}
			}
		case task.Type == taskFaceLiveness && task.State == StatePending && photoDone:
			if task.Attempts >= attemptsLimit-failedPhotos {
				return &Failure{Task: taskFaceLiveness, State: StateMaximumAttemptsExceeded, Attempts: task.Attempts}
			}
		}
	}
	return nil
}
