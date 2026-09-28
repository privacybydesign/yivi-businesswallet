package proofing

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

type webhookHealthResponse struct {
	State          string     `json:"state"`
	LastStatusCode *int       `json:"lastStatusCode,omitempty"`
	FailingSince   *time.Time `json:"failingSince,omitempty"`
	PendingRetries int        `json:"pendingRetries"`
}

// newWebhookHealthResponse shows the zero health as an endpoint not set up.
func newWebhookHealthResponse(h WebhookHealth) webhookHealthResponse {
	if h.State == "" {
		h.State = WebhookNotConfigured
	}
	return webhookHealthResponse(h)
}

// webhookResponse is a customer's endpoint; configured is false (and the rest
// empty) for a customer without one. Secret is set only in the answer that
// created the endpoint or rotated its secret.
type webhookResponse struct {
	Configured  bool                  `json:"configured"`
	URL         string                `json:"url,omitempty"`
	Events      []string              `json:"events"`
	SecretLast4 string                `json:"secretLast4,omitempty"`
	Secret      string                `json:"secret,omitempty"`
	Health      webhookHealthResponse `json:"health"`
	// AvailableEvents are the events an endpoint can subscribe to.
	AvailableEvents []string `json:"availableEvents"`
	// MaxAttempts is how often a delivery is tried before it is failed.
	MaxAttempts int `json:"maxAttempts"`
}

func (h *Handler) webhookResponse(r *http.Request, orgID uuid.UUID, w Webhook, secret string) (webhookResponse, error) {
	health, err := h.service.WebhookHealth(r.Context(), orgID)
	if err != nil {
		return webhookResponse{}, err
	}
	events := w.Events
	if events == nil {
		events = []string{}
	}
	return webhookResponse{
		Configured: w.URL != "", URL: w.URL, Events: events, SecretLast4: w.SecretLast4, Secret: secret,
		Health: newWebhookHealthResponse(health[w.CustomerID]), AvailableEvents: WebhookEvents, MaxAttempts: MaxWebhookAttempts,
	}, nil
}

func (h *Handler) getWebhook(w http.ResponseWriter, r *http.Request) error {
	id, err := customerIDFromPath(r)
	if err != nil {
		return err
	}
	orgID := orgFromRequest(r).ID
	hook, err := h.service.Webhook(r.Context(), orgID, id)
	if errors.Is(err, ErrWebhookNotFound) {
		hook, err = Webhook{CustomerID: id}, nil
	}
	if err != nil {
		return mapError(err)
	}
	out, err := h.webhookResponse(r, orgID, hook, "")
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

type saveWebhookRequest struct {
	URL    string   `json:"url"`
	Events []string `json:"events"`
}

func (h *Handler) saveWebhook(w http.ResponseWriter, r *http.Request) error {
	id, err := customerIDFromPath(r)
	if err != nil {
		return err
	}
	var body saveWebhookRequest
	if err := decode(r, &body); err != nil {
		return err
	}
	orgID := orgFromRequest(r).ID
	hook, secret, err := h.service.SaveWebhook(r.Context(), orgID, id, body.URL, body.Events)
	if err != nil {
		return mapError(err)
	}
	out, err := h.webhookResponse(r, orgID, hook, secret)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

func (h *Handler) rotateWebhookSecret(w http.ResponseWriter, r *http.Request) error {
	id, err := customerIDFromPath(r)
	if err != nil {
		return err
	}
	orgID := orgFromRequest(r).ID
	hook, secret, err := h.service.RotateWebhookSecret(r.Context(), orgID, id)
	if err != nil {
		return mapError(err)
	}
	out, err := h.webhookResponse(r, orgID, hook, secret)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}

func (h *Handler) removeWebhook(w http.ResponseWriter, r *http.Request) error {
	id, err := customerIDFromPath(r)
	if err != nil {
		return err
	}
	if err := h.service.RemoveWebhook(r.Context(), orgFromRequest(r).ID, id); err != nil {
		return mapError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) testWebhook(w http.ResponseWriter, r *http.Request) error {
	id, err := customerIDFromPath(r)
	if err != nil {
		return err
	}
	if err := h.service.SendTestWebhook(r.Context(), orgFromRequest(r).ID, id); err != nil {
		return mapError(err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

type deliveryResponse struct {
	ID             uuid.UUID  `json:"id"`
	Event          string     `json:"event"`
	SessionID      *uuid.UUID `json:"sessionId,omitempty"`
	Status         string     `json:"status"`
	Attempts       int        `json:"attempts"`
	LastStatusCode *int       `json:"lastStatusCode,omitempty"`
	LastError      string     `json:"lastError,omitempty"`
	LastAttemptAt  *time.Time `json:"lastAttemptAt,omitempty"`
	DeliveredAt    *time.Time `json:"deliveredAt,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
}

func (h *Handler) listWebhookDeliveries(w http.ResponseWriter, r *http.Request) error {
	id, err := customerIDFromPath(r)
	if err != nil {
		return err
	}
	deliveries, err := h.service.WebhookDeliveries(r.Context(), orgFromRequest(r).ID, id)
	if err != nil {
		return mapError(err)
	}
	out := make([]deliveryResponse, 0, len(deliveries))
	for _, d := range deliveries {
		out = append(out, deliveryResponse{
			ID: d.ID, Event: d.Event, SessionID: d.RequestID, Status: d.Status, Attempts: d.Attempts,
			LastStatusCode: d.LastStatusCode, LastError: d.LastError, LastAttemptAt: d.LastAttemptAt,
			DeliveredAt: d.DeliveredAt, CreatedAt: d.CreatedAt,
		})
	}
	respond.JSON(w, r, http.StatusOK, out)
	return nil
}
