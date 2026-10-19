package proofing

import (
	"net/http"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// maxFormBodyBytes caps a small JSON form a page posts, such as an address:
// far above what its fields allow, read before they are checked.
const maxFormBodyBytes = 16 << 10

type flowAddressBody struct {
	AddressMode AddressMode `json:"addressMode"`
}

func (h *Handler) getFlowAddress(w http.ResponseWriter, r *http.Request) error {
	mode, err := h.service.FlowAddress(r.Context(), orgFromRequest(r), r.PathValue("flowID"))
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, flowAddressBody{AddressMode: mode})
	return nil
}

func (h *Handler) saveFlowAddress(w http.ResponseWriter, r *http.Request) error {
	var body flowAddressBody
	if err := decode(r, &body); err != nil {
		return err
	}
	mode, err := h.service.SaveFlowAddress(r.Context(), orgFromRequest(r), r.PathValue("flowID"), body.AddressMode)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, flowAddressBody{AddressMode: mode})
	return nil
}

// addressModeOf is the request's mode, off for a row from before the address.
func addressModeOf(req Request) AddressMode {
	if req.Address == "" {
		return AddressOff
	}
	return req.Address
}

// addressUntil is when the subject can last enter their address: AddressWindow
// after the approval of a request that asks for it and holds none yet, nil
// otherwise.
func addressUntil(req Request) *time.Time {
	if !req.Address.asked() || req.SubjectAddress != nil || req.Status != StatusApproved || req.CompletedAt == nil {
		return nil
	}
	until := req.CompletedAt.Add(AddressWindow)
	return &until
}

// addAddress keeps the address entered on a request's on-screen page; the
// page reads it back with the request.
func (h *Handler) addAddress(w http.ResponseWriter, r *http.Request) error {
	id, requestedBy, err := sentRequestTarget(r)
	if err != nil {
		return err
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBodyBytes)
	var body Address
	if err := decode(r, &body); err != nil {
		return err
	}
	if _, err := h.service.AddAddress(r.Context(), orgFromRequest(r).ID, id, requestedBy, body); err != nil {
		return mapError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// hostedAddAddress keeps the address a hosted link's subject entered. The
// answer carries no address: the link's token alone opens the page.
func (h *Handler) hostedAddAddress(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBodyBytes)
	var body Address
	if err := decode(r, &body); err != nil {
		return err
	}
	if _, err := h.service.HostedAddAddress(r.Context(), r.PathValue("token"), body); err != nil {
		return mapError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
