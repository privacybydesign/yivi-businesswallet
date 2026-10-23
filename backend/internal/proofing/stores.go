package proofing

import (
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// NewStores builds every store the service runs on. The requests and webhooks
// stores come in built, as the API server also uses them on their own.
func NewStores(db database.DB, recorder audit.Recorder, cipher *crypto.Cipher, requests *RequestStore, webhooks *WebhookStore) Stores {
	return Stores{
		Settings:       NewSettingsStore(db, recorder),
		Requests:       requests,
		Customers:      NewCustomerStore(db, recorder),
		APIKeys:        NewAPIKeyStore(db, recorder),
		Webhooks:       webhooks,
		Events:         audit.NewReader(db),
		Pauses:         NewPauseStore(db, recorder),
		FlowHosted:     NewFlowHostedStore(db, recorder),
		FlowDiplomas:   NewFlowDiplomaStore(db, recorder),
		Diplomas:       NewDiplomaStore(db, recorder),
		FlowAddresses:  NewFlowAddressStore(db, recorder),
		FlowEmployment: NewFlowEmploymentStore(db, recorder),
		DataRequests:   NewDataRequestStore(db, recorder, cipher),
	}
}
