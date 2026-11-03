package idscan

import (
	"context"
	"errors"
)

const (
	taskFaceLiveness = "face_liveness"
	taskFaceMatch    = "face_match"

	// fileCategoryImage is the liveness check's still of the person's face.
	// Its other file, a video, is never fetched.
	fileCategoryImage = "image"
)

// faceTasks are the face checks, in the order ID Scan takes them: a liveness
// check, then a match of its still against the document's portrait. ID Scan
// takes face_match only with face_liveness, and both only with document_scan.
var faceTasks = []string{taskFaceLiveness, taskFaceMatch}

// ErrNoSelfie is a passed liveness check whose result holds no still.
var ErrNoSelfie = errors.New("idscan: the liveness check holds no still")

// Face is the face checks' outcome on a completed scan: the liveness check
// passed, or the scan would have failed (TransactionStatus).
type Face struct {
	// MatchScore is ID Scan's similarity of the live face to the document's
	// portrait, 0 to 1; nil when it gave none.
	MatchScore *float64
	// Selfie is the liveness check's still.
	Selfie *Image
}

// readFace is the face checks' outcome in a completed scan's tasks, and the
// liveness still's file; nil when no face check was asked.
func readFace(tasks []taskResult) (*Face, *file) {
	var face *Face
	var still *file
	for _, task := range tasks {
		switch task.Type {
		case taskFaceLiveness:
			if face == nil {
				face = &Face{}
			}
			still = livenessStill(task)
		case taskFaceMatch:
			if face == nil {
				face = &Face{}
			}
			face.MatchScore = task.Score
		}
	}
	return face, still
}

// livenessStill is the liveness task's still file; nil when it holds none.
func livenessStill(task taskResult) *file {
	for _, f := range task.Files {
		if f.Category == fileCategoryImage {
			return &f
		}
	}
	return nil
}

// downloadSelfie fetches the liveness check's still.
func (c *Client) downloadSelfie(ctx context.Context, still *file) (*Image, error) {
	if still == nil {
		return nil, ErrNoSelfie
	}
	img, err := c.download(ctx, *still)
	if err != nil {
		return nil, err
	}
	return &img, nil
}
