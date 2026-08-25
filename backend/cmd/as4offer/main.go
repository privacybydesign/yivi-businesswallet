// Command as4offer submits an OpenID4VCI credential offer to a business wallet
// organization over AS4/QERDS, by handing it to an eDelivery access point's
// Domibus WS plugin.
//
// It plays the role of a remote issuing platform's backend (ver.id, in the
// two-gateway bench): the thing that has just minted an offer at its own issuer
// and now needs to deliver it. It is therefore two things at once —
//
//  1. the driver for the `verid` compose profile's end-to-end test, and
//  2. the reference for a partner implementing the sending side, since it shows
//     exactly which ebMS3 addressing and which payload the wallet expects.
//
// It deliberately reuses internal/qerdsprovider and internal/attestation rather
// than re-deriving the wire format, so the envelope it sends cannot drift from
// the one the wallet parses on receipt.
//
//	# against the bench's simulated ver.id gateway (`verid` profile)
//	go run ./cmd/as4offer \
//	  -endpoint http://localhost:8091/domibus/services/backend \
//	  -recipient acme@qerds.localhost \
//	  -offer 'openid-credential-offer://?credential_offer=%7B...%7D' \
//	  -name 'Bewijs van inschrijving'
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("as4offer", flag.ExitOnError)

	// Which access point to submit through. This is the SENDER's own gateway —
	// ver.id submits to ver.id's Domibus, which then pushes AS4 to ours.
	endpoint := fs.String("endpoint", "http://localhost:8091/domibus/services/backend",
		"Domibus WS-plugin backend URL of the SENDING access point")
	authToken := fs.String("auth-token", "", "bearer token for the WS plugin, if it requires one")

	// ebMS3 addressing. Defaults match docker/development/domibus/pmode-verid.xml.
	fromParty := fs.String("from-party", "verid", "ebMS3 From party id (the sender's own party)")
	toParty := fs.String("to-party", "domibus-blue", "ebMS3 To party id (the wallet's gateway)")
	partyType := fs.String("party-type", "urn:oasis:names:tc:ebcore:partyid-type:unregistered",
		"ebMS3 party id type; a real deployment uses a registered scheme (e.g. ISO 6523)")
	service := fs.String("service", "bdx:noprocess", "ebMS3 Service")
	serviceType := fs.String("service-type", "tc1", "ebMS3 Service type")
	action := fs.String("action", "TC1Leg1", "ebMS3 Action")

	// Business addressing: these are the QERDS digital addresses, not parties.
	// The recipient is how the wallet resolves WHICH organization gets the offer.
	sender := fs.String("sender", "verid@partners.qerds.localhost",
		"originalSender: the partner's QERDS digital address")
	recipient := fs.String("recipient", "",
		"finalRecipient: the receiving organization's QERDS digital address (required)")

	// The offer itself.
	offer := fs.String("offer", "",
		"the OpenID4VCI credential offer: openid-credential-offer:// deeplink or https offer URI (required)")
	credentialName := fs.String("name", "", "credential name shown to the receiving organization")
	orgName := fs.String("org", "Ver.ID", "sending organization's display name")

	pingOnly := fs.Bool("ping", false, "only probe the WS plugin and exit")
	timeout := fs.Duration("timeout", 60*time.Second, "overall timeout")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}

	provider := qerdsprovider.NewDomibusProvider(
		*endpoint,
		qerdsprovider.NewTokenAuthenticator(*authToken),
		qerdsprovider.DomibusConfig{
			FromParty:   *fromParty,
			ToParty:     *toParty,
			PartyType:   *partyType,
			Service:     *service,
			ServiceType: *serviceType,
			Action:      *action,
		},
		&http.Client{Timeout: *timeout},
	)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if *pingOnly {
		// Probe only: a WS plugin that is not serving yet produces a much less
		// obvious failure from submitMessage than from a WSDL fetch.
		if err := provider.Ping(ctx); err != nil {
			return fmt.Errorf("access point not reachable at %s: %w", *endpoint, err)
		}
		fmt.Printf("access point OK: %s\n", *endpoint)
		return nil
	}

	// Validate the required flags before touching the network, so a bare
	// `go run ./cmd/as4offer` says what is missing rather than how it failed to
	// connect.
	if *recipient == "" {
		return errors.New("-recipient is required (the organization's QERDS digital address)")
	}
	if *offer == "" {
		return errors.New("-offer is required (the OpenID4VCI credential offer)")
	}

	// Probe before submitting: a WS plugin that is not serving yet produces a
	// much less obvious failure from submitMessage than from a WSDL fetch.
	if err := provider.Ping(ctx); err != nil {
		return fmt.Errorf("access point not reachable at %s: %w", *endpoint, err)
	}

	// Built with the wallet's own marshaller, so the body is exactly the
	// eaa-credential-offer/v1 envelope attestation.OfferReceiver looks for. A
	// partner reimplementing this must produce the same JSON — see
	// internal/attestation/offer_envelope.go and partners/verid/README.md.
	body, err := attestation.MarshalCredentialOfferEnvelope(*orgName, *credentialName, *offer)
	if err != nil {
		return fmt.Errorf("build offer envelope: %w", err)
	}

	receipt, err := provider.Send(ctx, qerdsprovider.OutboundMessage{
		Sender:    qerdsprovider.Address(*sender),
		Recipient: qerdsprovider.Address(*recipient),
		Subject:   offerSubject(*credentialName),
		Body:      body,
	})
	if err != nil {
		return fmt.Errorf("submit to access point: %w", err)
	}

	fmt.Printf("submitted\n  messageId: %s\n  status:    %s\n  from:      %s (%s)\n  to:        %s (%s)\n",
		receipt.ProviderRef, receipt.Status, *sender, *fromParty, *recipient, *toParty)
	fmt.Printf("\nSubmission is accepted, not delivered: AS4 delivery is asynchronous.\n" +
		"Check the sending gateway's message log for SENT/ACKNOWLEDGED, and the\n" +
		"receiving wallet for the inbound message and the held credential.\n")
	return nil
}

func offerSubject(credentialName string) string {
	if credentialName == "" {
		return "Credential offer"
	}
	return "Credential offer: " + credentialName
}
