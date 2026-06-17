# Yivi Business Wallet

A **European Business Wallet (EBW)** implementation built on [Yivi](https://yivi.app)'s
[`irmago`](https://github.com/privacybydesign/irmago) libraries, deployed on
**sovereign, EU-based hosting**.

This project aims to deliver a working, standards-aligned Business Wallet for **economic
operators** (companies, SMEs, sole traders) and **public sector bodies**, in line with the
proposed EU Regulation on the establishment of European Business Wallets — **COM(2025) 838
final, 2025/0358 (COD)** (19 November 2025), which builds on the eIDAS / European Digital
Identity Framework (Regulation (EU) No 910/2014).

> ⚠️ **Status:** early-stage. COM(2025) 838 is a Commission *proposal* still in the ordinary
> legislative procedure — provisions may change before adoption. We track it and adapt.
> See [`regulation/FEATURE_LIST.md`](regulation/FEATURE_LIST.md) for the extracted feature
> breakdown with article references.

---

## What we aim to achieve

A trusted, user-friendly digital wallet that lets businesses and public bodies **identify,
authenticate, sign, seal, and securely exchange verified data and documents across EU
borders with full legal effect** — without depending on non-EU infrastructure.

Concretely, we are working toward the **core functionalities** the regulation requires
(Article 5) and the **technical features** (Article 6):

- **Verifiable identity & attestations** — issue, store, select, combine, and present
  electronic attestations of attributes (EAAs) for a legal entity.
- **Selective disclosure / data minimisation** — share only the attributes a relying party
  needs. This is where Yivi/`irmago`'s attribute-based credential model is a natural fit.
- **Qualified signing & sealing** — qualified electronic signatures and seals (Art 5(1)(d))
  and qualified time stamps (Art 5(1)(e)), via integration with qualified trust service
  providers.
- **Secure exchange channel** — qualified electronic registered delivery service (QERDS)
  for submitting documents and sending/receiving notifications (Art 5(1)(i)).
- **Representation & mandates** — multi-user access with verifiable, auditable, revocable
  authorisations, with conflicts and over-delegation detected in real time (Art 5(1)(j),
  Art 6(2)(b)).
- **Automated, machine-to-machine interaction** — APIs for system-to-system exchange
  without manual intervention (Art 6(1)(d)).
- **Interoperability** — wallet-to-wallet and wallet-to-EUDI-wallet exchange, the European
  Unique Identifier (EUID), and the European Digital Directory (Art 9, Art 10).

The full mapping of features to articles lives in
[`regulation/FEATURE_LIST.md`](regulation/FEATURE_LIST.md).

### Design principles

- **EU digital sovereignty** — sovereign hosting, EU-established operation, no dependence on
  high-risk or third-country-controlled providers (mirrors the provider requirements in
  Art 7).
- **Security & privacy by design** — selective disclosure, data minimisation, GDPR
  alignment (Regulation (EU) 2016/679), security-by-design (Art 6(2)(c)).
- **Open standards & technology neutrality** — built on the eIDAS trust framework and open
  protocols; the regulation deliberately mandates a common layer rather than a single design.
- **Build on proven open source** — reuse Yivi/IRMA's battle-tested attribute-based identity
  stack rather than reinventing credential issuance and disclosure.

---

## Why Yivi / `irmago`

[Yivi](https://yivi.app) (formerly IRMA) is an open-source, EU-origin identity wallet from
the Privacy by Design Foundation, with [`irmago`](https://github.com/privacybydesign/irmago)
as its Go implementation of the IRMA attribute-based credential protocol. It already provides:

- Attribute-based credentials with **selective disclosure** and **unlinkability**.
- An issuance/verification protocol and server components in Go — aligning with our backend
  stack.
- A privacy-first design philosophy that maps cleanly onto the regulation's selective
  disclosure and data-minimisation requirements.

Yivi/IRMA targets **natural persons**. A key part of this project is **extending the model
to legal persons / economic operators** — owner identification data tied to the EUID,
representation and mandates, qualified signing/sealing, and the QERDS channel — to meet the
EBW requirements that go beyond what a citizen wallet covers.

---

## Tech stack

| Layer | Technology |
|---|---|
| Frontend | **React** — wallet front-end (web UI) for owners and authorised representatives |
| Backend | **Go (Golang)** — wallet back-end, building on Yivi `irmago`; APIs and system-to-system interfaces |
| Data | **PostgreSQL** — owner data, attestations metadata, mandates, audit/transaction logs |
| Runtime | **Kubernetes** — orchestration on sovereign EU hosting |
| Trust services | Integration with EU/NL **qualified trust service providers** (qualified certificates, qualified timestamps, QERDS) |

**Infrastructure** (Kubernetes manifests, sovereign hosting setup, networking, secrets,
observability) lives in a **separate repository** — this repo contains the application
(React front-end + Go back-end + database schema).

---

## Repository layout

```
.
├── README.md
├── regulation/                  # The regulation and its extracted feature list
│   ├── COM_2025_838_act.pdf     # Main legislative act (proposal)
│   ├── COM_2025_838_annex.pdf   # Technical annex
│   └── FEATURE_LIST.md          # Features mapped to articles + NL trust providers
└── Yivi Design System.zip       # Design system assets for the front-end
```

> Application source (React front-end, Go back-end, DB migrations) will be added as the
> project takes shape. Infrastructure-as-code is maintained in the separate infra repo.

---

## Trust service providers

The wallet relies on eIDAS qualified trust services for signing, sealing, timestamps, and
the QERDS channel. The candidate **Dutch** qualified providers (from the official NL trusted
list) and which wallet functionality each supplies are listed in
[`regulation/FEATURE_LIST.md`](regulation/FEATURE_LIST.md#parties-we-would-need-netherlands).
QERDS is the scarcest capability and is worth securing early.

---

## Roadmap (high level)

1. **Foundations** — Go back-end skeleton on `irmago`, PostgreSQL schema, React shell, local
   dev environment.
2. **Identity & attestations** — legal-entity owner identification data, EUID integration,
   issue/store/present EAAs with selective disclosure.
3. **Signing & sealing** — qualified e-signature/seal and qualified timestamp integration
   with a qualified trust service provider.
4. **Secure exchange** — QERDS integration and the secure communication channel.
5. **Representation & mandates** — multi-user authorisation with real-time delegation checks.
6. **Interoperability** — wallet-to-wallet and EUDI-wallet exchange, European Digital
   Directory interface.
7. **Compliance & hardening** — Annex technical requirements, NIS2 alignment, audit logging,
   security review.

---

## License

See [`LICENSE`](LICENSE).
