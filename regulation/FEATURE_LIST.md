# European Business Wallets — Feature List

**Source:** Proposal for a Regulation of the European Parliament and of the Council on the establishment of European Business Wallets
**Reference:** COM(2025) 838 final — 2025/0358 (COD)
**Date:** Brussels, 19.11.2025
**Legal basis:** Article 114 TFEU (internal market)
**Builds on:** European Digital Identity Framework — Regulation (EU) No 910/2014 (eIDAS), as amended by Regulation (EU) 2024/1183

Local copies in this folder:
- `COM_2025_838_act.pdf` — main legislative act (93 pp.)
- `COM_2025_838_annex.pdf` — technical annex (8 pp.)

---

## What it is

A **European Business Wallet (EBW)** is a digital wallet for **economic operators** (companies, SMEs, sole traders) and **public sector bodies** to securely identify, authenticate, sign/seal, and exchange verified data, documents and attestations with full legal effect across EU borders. It complements the citizen-facing European Digital Identity Wallet with functionality tailored to B2G and B2B interactions. The instrument is **technology-neutral and market-driven** — it sets a common minimum layer, not a single rigid design.

---

## Core functionalities (Article 5(1))

Providers must enable wallet owners to:

- **(a)** Securely issue, request, obtain, select, combine, store, delete, share and present electronic attestations of attributes (EAAs)
- **(b)** Selectively disclose owner identification data and attributes (data minimisation)
- **(c)** Request and share owner ID data and EAAs securely between Business Wallets, EU Digital Identity Wallets, and relying parties
- **(d)** Sign with qualified electronic signatures and seal with qualified electronic seals
- **(e)** Bind data to a particular time with qualified electronic time stamps
- **(f)** Issue EAAs to Business Wallets and EU Digital Identity Wallets
- **(g)** Issue chained/linked attestations (attestation can be linked to other relevant attestations)
- **(h)** Use qualified and non-qualified EAAs so owners and authorised representatives can authenticate themselves
- **(i)** Transmit/receive documents and data via a **qualified electronic registered delivery service (QERDS)** supporting confidentiality and integrity
- **(j)** Authorise multiple users to access/operate the wallet, with owner able to manage and revoke authorisations (mandate / representation management)
- **(k)** Authorise relying parties to request EAAs, with owner able to manage and revoke those authorisations
- **(l)** Export their data (ID data, EAAs, communication logs, interaction records) in a structured, machine-readable format on request or on service termination
- **(m)** Access a log of all transactions
- **(n)** Access a common dashboard for accessing, storing and verifying QERDS communications

Additional notes:
- **Additional functionalities** beyond the core set are permitted, provided they don't compromise confidentiality, availability, integrity, reliability or interoperability (Art 5(2))
- **QERDS as a standalone service** must be offered to EU Digital Identity Wallet users — so sole traders/self-employed can use the secure channel without a full Business Wallet (Art 5(3))

---

## Technical features (Article 6)

Common protocols and interfaces for:
- **(a)** Issuance of owner ID data, qualified/non-qualified EAAs, and certificates to wallets
- **(b)** Relying parties to request and validate owner ID data and EAAs
- **(c)** Sharing/presenting owner ID data, EAAs and selectively disclosed data to relying parties
- **(d)** **Automated interaction** — machine-to-machine, without manual intervention or direct user action
- **(e)** Secure **remote onboarding** of the owner via an authorised representative (eIDAS assurance level "substantial" or "high")
- **(f)** Wallet-to-wallet and wallet-to-EUDI-wallet interaction for secure receive/validate/share
- **(g)** Authenticating relying parties
- **(h)** Verifying authenticity and validity of Business Wallets
- **(i)** Providing the QERDS, including an interface to the European Digital Directory
- **(j)** Assigning **at least one unique digital address** to each owner (for QERDS and the Directory)
- **(k)** Providing wallet unit attestations (public/private keys protected by a secure cryptographic device)
- **(l)** Managing critical assets via wallet secure cryptographic application/device at assurance level "substantial"

Additional technical guarantees (Art 6(2)):
- Owner ID data digitally associated with the wallet
- For multi-user authorisation: role/attribute mappings **verifiable, auditable, revocable, traceable**; conflicts, over-delegation and expired authorisations **automatically detected and prevented in real time**; authorisation logic interoperable across Member States
- **Security-by-design**
- Validation mechanisms to verify wallet authenticity/validity
- Mechanism for owners to request technical support and report incidents
- **Revocation** of wallet validity (on owner request, on security compromise, on cessation of activity, or if provider drops off the trusted list)

---

## Identity & directory infrastructure

- **Owner identification data (Art 8):** issued to each wallet as qualified EAAs, public-sector authentic-source attestations, or Commission-issued attestations; contains at least the official name plus the unique identifier
- **Unique identifiers (Art 9):** reuse the **European Unique Identifier (EUID)** where one exists; otherwise a unique identifier is created from national registers — guaranteeing Union-wide uniqueness; no owner gets more than one
- **European Digital Directory (Art 10):** a Commission-run, maintained trusted directory (web application) with **two interfaces** — (a) a machine-readable **API** for system-to-system communication, and (b) a secure web portal for authenticated users — enabling operators and public bodies to be contacted easily

---

## Provider regime

- **Notification-based entry (Art 11):** entities notify a competent supervisory body; ~30-day review; qualified trust service providers fast-tracked; right to judicial remedy
- **Trusted list (Art 12):** Commission maintains a machine-readable public list of notified providers
- **Provider requirements (Art 7):** must be EU-established with principal operations in the Union and not under third-country control; comply with eIDAS Art 19a, NIS2 Directive (EU) 2022/2555, and high-risk-supplier rules; ensure confidentiality/integrity/authenticity/interoperability/availability; transparent T&Cs; data portability on termination

---

## Governance & supervision

- **National supervisory bodies (Art 13):** the existing eIDAS supervisory bodies; **ex-post supervision** (not prior authorisation), investigation, penalties, breach reporting, list revocation
- **Penalties (Art 13(6)–(9)):** effective/proportionate/dissuasive; administrative fines up to **2% of total worldwide annual turnover**
- **Commission emergency intervention (Art 13(10)–(13))** for non-compliance threatening the internal market
- **European Digital Identity Cooperation Group (Art 14):** coordination, best-practice sharing
- **Union entities (Art 15):** Commission acts as their supervisory body

---

## Acceptance obligations on the public sector (Chapter III, Article 16)

- Within **24 months** of entry into force, public sector bodies must **enable economic operators** to use core functionalities to: **(a) identify & authenticate, (b) sign or seal, (c) submit documents, (d) send/receive notifications** — for reporting obligations or administrative procedures
- For submission and notifications, public bodies must themselves hold a Business Wallet and use the QERDS
- **Transitional derogation** until 36 months: public bodies may use alternative eIDAS-compliant QERDS solutions with a gateway, before fully adopting Business Wallets
- **No obligation is imposed on economic operators** — adoption is voluntary for businesses

---

## International dimension (Chapter IV)

- **Equivalence recognition (Art 17):** Commission may recognise third-country business wallets/frameworks offering equivalent assurances, interoperable with the eIDAS trust framework; published list; can be suspended/repealed
- **Issuance to non-EU operators (Art 18):** providers may issue Business Wallets to economic operators established outside the Union, subject to identity proofing per eIDAS Art 24(1a) and a one-set-per-operator rule

---

## Cross-cutting principles

- **Principle of legal equivalence (Art 4):** an action performed through a Business Wallet (or QERDS by sole traders/self-employed) has the **same legal effect** as if done in person, on paper, or by any other compliant means
- **Selective disclosure / data minimisation** and GDPR alignment (Regulation (EU) 2016/679)
- **Interoperability** with eIDAS trust services and authentic sources; reuse of EUID, BRIS, BORIS
- **Complementarity** with the Single Digital Gateway / Once-Only Technical System, Digital Product Passport, Interoperable Europe Act, the upcoming 28th-regime company framework, and VAT in the Digital Age (ViDA)
- Detailed core/technical requirements expanded in the **Annex**, with **implementing acts** to set reference standards (committee procedure, Art 19)

---

## Parties we would need (Netherlands)

The Business Wallet's core functionalities lean directly on **eIDAS qualified trust services**: qualified signatures/seals (Art 5(1)(d)) need qualified certificates, qualified time stamps (Art 5(1)(e)) need a QTSA, and the secure channel / QERDS (Art 5(1)(i), Art 16) needs a qualified registered-delivery provider. Qualified trust service providers also get a fast-track into the EBW provider list (Art 7(3), Art 11(3)).

Below are the **currently qualified Dutch trust service providers** from the official NL trusted list (`source: rdi.nl current-tsl.xml`, referenced by the EU LOTL — the same data behind the eIDAS dashboard URL provided), grouped by the wallet functionality they would supply.

### Qualified certificates — for electronic signatures & seals → Art 5(1)(d)
- CIBG (Ministerie van VWS — UZI register)
- Digidentity B.V.
- Diginotar B.V.
- ESG de Electronische Signatuur B.V.
- Getronics Nederland B.V.
- KPN B.V.
- KPN Corporate Market B.V.
- Ministerie van Defensie
- Ministerie van Infrastructuur en Waterstaat
- DigiCert Europe Netherlands B.V.
- Cleverbase ID B.V.
- NID TSP B.V.

### Qualified time stamping (QTST) → Art 5(1)(e)
- DigiCert Europe Netherlands B.V.

### Qualified electronic registered delivery service (QERDS) → Art 5(1)(i), Art 16
- Aangetekend B.V.
- Secumail B.V.

> Notes:
> - DigiCert Europe Netherlands B.V. is the only NL provider currently qualified for **both** qualified certificates **and** qualified timestamps.
> - QERDS is the scarcest capability (only two qualified NL providers) yet is central to the public-sector submission/notification obligations — worth securing early.
> - This list reflects the live NL trusted list at retrieval time; the eIDAS trusted lists are updated continuously, so re-check before committing to a provider.

---

## Key timeline

| Milestone | Timing |
|---|---|
| Entry into force | 20th day after publication in the OJEU |
| Application | 1 year after entry into force |
| Public-sector acceptance obligation | 24 months after entry into force |
| End of transitional QERDS derogation | 36 months after entry into force |
| Member State penalty rules notified | 12 months after entry into force |
| Commission evaluation & review report | 3 years after entry into force |
