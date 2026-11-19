package proofing

import (
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// NewStores builds every store the service runs on. The requests and webhooks
// stores come in built, as the API server also uses them on their own.
func NewStores(db database.DB, recorder audit.Recorder, cipher *crypto.Cipher, requests *RequestStore, webhooks *WebhookStore) Stores {
	// One request store for both, so its lock pool covers the matching lock too.
	dataRequests := NewDataRequestStore(db, recorder, cipher)
	if requests != nil {
		dataRequests.requests = requests
	}
	return Stores{
		Settings:       NewSettingsStore(db, recorder),
		Requests:       requests,
		OrgSettings:    NewOrgSettingsStore(db, recorder),
		APIKeys:        NewAPIKeyStore(db, recorder, cipher),
		Webhooks:       webhooks,
		Events:         audit.NewReader(db),
		Pauses:         NewPauseStore(db, recorder),
		FlowHosted:     NewFlowHostedStore(db, recorder),
		FlowBranding:   NewFlowBrandingStore(db, recorder),
		FlowDiplomas:   NewFlowDiplomaStore(db, recorder),
		Diplomas:       NewDiplomaStore(db, recorder),
		Unchecked:      NewUncheckedDocumentStore(db, recorder, cipher),
		FlowAddresses:  NewFlowAddressStore(db, recorder),
		FlowEmployment: NewFlowEmploymentStore(db, recorder),
		DataRequests:   dataRequests,
		ReviewLinks:    NewReviewLinkStore(db, recorder),
	}
}
