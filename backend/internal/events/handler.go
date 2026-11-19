package events

import (
	"net/http"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// Handler serves an organisation's event stream to its members.
type Handler struct {
	hub         *Hub
	requireUser func(http.Handler) http.Handler
	authorize   func(http.Handler) http.Handler
}

func NewHandler(hub *Hub, requireUser, authorize func(http.Handler) http.Handler) *Handler {
	return &Handler{hub: hub, requireUser: requireUser, authorize: authorize}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("GET /orgs/{slug}/events", h.requireUser(h.authorize(respond.HandlerFunc(h.orgEvents))))
}

// orgEvents streams every kind of change in the member's organisation, so one
// connection keeps all of its pages current.
func (h *Handler) orgEvents(w http.ResponseWriter, r *http.Request) error {
	orgID := organization.OrgFromContext(r.Context()).ID
	topics := make([]string, len(OrgKinds))
	for i, kind := range OrgKinds {
		topics[i] = Topic(kind, orgID)
	}
	return h.hub.Serve(w, r, topics...)
}
