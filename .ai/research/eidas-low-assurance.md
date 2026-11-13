# eIDAS-niveau Low bij identiteitsvaststelling van een natuurlijk persoon

Onderzoek voor de beslissing in GitHub privacybydesign/yivi-businesswallet#305 en Linear YIV-369: welke
stappen en checks Low vereist (en dus in de flow-editor vastzet), en welke regel tekst de admin erbij ziet.
Stand van 2026-10-19. De engine is gelezen op `origin/main`
(`backend/internal/proofingengine/flow/flow.go`, `LevelRequirements`). Citaten uit bronnen staan in de
originele taal. Een bronnenlijst met URL's staat onderaan; `[Bn]` verwijst daarnaar.

## Kort antwoord

- Voor Low eist Uitvoeringsverordening (EU) 2015/1502, bijlage §2.1.2, drie dingen. De persoon
  "can be assumed to be in possession of" erkend bewijs. Het bewijs "can be assumed to be genuine […]
  and […] appears to be valid". Een gezaghebbende bron kent de identiteit, en "it may be assumed that
  the person claiming the identity is one and the same" [B2].
- Bezit en binding worden op Low dus **verondersteld, niet geverifieerd**. Binding (is dit de persoon
  van het document?) hoeft op Low niet gecontroleerd te worden. Een gezichtscheck is niet vereist.
- Onze definitie (chip lezen, Passive Authentication slaagt, verlopen documenten geweigerd) **voldoet
  aan Low** voor het identiteitsvaststellingsdeel. Op echtheid zit ze er ruim boven: de
  handtekeningcontrole van de uitgever is volgens de officiële LoA-guidance "best practice", en een
  "elaborate check" is op Low niet nodig [B10]. Ze schiet nergens tekort.
- De restrisico's zijn precies wat Low toestaat. Passive Authentication "does not prevent exact copying
  of the contactless IC's content or chip substitution" [B12], en zonder gezichtscheck is de persoon die
  scant niet aan het document gebonden.
- Voorstel: Low zet alleen de stap "Document and chip" (`nfc_read`) met check `nfc.passive_auth` vast.
  De rest blijft vrij. De editortekst: *"The person scans the chip in their passport or ID card with
  their phone. No selfie."*
- Naamgeving: "Low" naast "eIDAS Substantial" is inconsistent. Beide niveaus claimen alleen het
  identiteitsvaststellingsdeel van eIDAS, niet een volledig eIDAS-niveau (zie §5).

## 1. Wat vereist Low minimaal?

### 1.1 Waar de eisen staan

- **eIDAS art. 8(2)(a)** (ongewijzigd in de geconsolideerde tekst na Verordening 2024/1183) definieert
  Low als een middel dat "provides a limited degree of confidence in the claimed or asserted identity of
  a person, and is characterised with reference to technical specifications, standards and procedures
  related thereto, including technical controls, the purpose of which is to decrease the risk of misuse
  or alteration of the identity" [B1].
- **Art. 8(3)(a)** laat de Commissie minimumeisen stellen aan "the procedure to prove and verify the
  identity of natural or legal persons applying for the issuance of electronic identification means"
  [B1]. Dat is gedaan in **Uitvoeringsverordening (EU) 2015/1502** [B2].
- 2015/1502 is sinds 2015 inhoudelijk niet gewijzigd. De enige wijziging, 2022/960, corrigeert alleen
  de Tsjechische taalversie: "(Does not concern the English language.)" [B3]. ETSI bevestigt: "CIR (EU)
  2015/1502 [i.3] has not changed with the amended eIDAS regulation [i.25]" [B9].
- Twee regels uit art. 1 van 2015/1502 doen ertoe [B2]:
  - art. 1(3): "When the electronic identification means […] meets a requirement listed in a higher
    assurance level then it shall be presumed to fulfil the equivalent requirement of a lower assurance
    level." Meer doen dan Low vraagt is dus nooit fout.
  - art. 1(4): "all elements listed in the Annex for a particular assurance level […] shall be met in
    order to match the claimed assurance level." Een volledig eIDAS-niveau omvat ook uitgifte (§2.2),
    authenticatie (§2.3) en organisatie (§2.4). Zie §5 over naamgeving.

### 1.2 De tekst voor Low (bijlage 2015/1502)

§2.1.1 "Application and registration", Low [B2]:

> 1. Ensure the applicant is aware of the terms and conditions related to the use of the electronic
>    identification means.
> 2. Ensure the applicant is aware of recommended security precautions related to the electronic
>    identification means.
> 3. Collect the relevant identity data required for identity proofing and verification.

§2.1.2 "Identity proofing and verification (natural person)", Low [B2]:

> 1. The person can be assumed to be in possession of evidence recognised by the Member State in which
>    the application for the electronic identity means is being made and representing the claimed
>    identity.
> 2. The evidence can be assumed to be genuine, or to exist according to an authoritative source and the
>    evidence appears to be valid.
> 3. It is known by an authoritative source that the claimed identity exists and it may be assumed that
>    the person claiming the identity is one and the same.

De Nederlandse versie is even authentiek [B2-NL]:

> 1. De persoon kan worden verondersteld in het bezit te zijn van bewijs dat wordt erkend door de
>    lidstaat waar de aanvraag voor het elektronische identificatiemiddel wordt gedaan, en dat de
>    opgegeven identiteit vertegenwoordigt.
> 2. Het bewijs kan worden verondersteld echt te zijn, dan wel volgens een gezaghebbende bron te bestaan,
>    en het bewijs lijkt geldig te zijn.
> 3. Een gezaghebbende bron weet dat de opgegeven identiteit bestaat en er kan worden verondersteld dat
>    de persoon die de identiteit opgeeft, dezelfde persoon is.

### 1.3 Per element: bezit, echtheid, binding

De eIDAS Cooperation Network heeft een officiële interpretatie gepubliceerd: de "Guidance for the
application of the levels of assurance which support the eIDAS Regulation" (versie 2021, gepubliceerd op
de Commissie-wiki [B10]). De voorbeelden daarin zijn "not normative or exclusive" [B10], maar het is
de interpretatie waar ENISA ook naar verwijst [B11].

**Bezit (element 1): verondersteld, niet geverifieerd.**
- Guidance: "Evidence can be reasonably assumed to be in possession of the subject, if there are no
  indications to the contrary, and experience assures that the evidence supporting the assumption is
  sufficient in practice." Als voorbeeld: "Possession can be assured by presenting the evidence
  physically or remotely (e.g., electronically)." [B10]
- Ter vergelijking: Substantial eist "The person has been verified to be in possession of evidence" [B2].

**Echtheid en geldigheid (element 2): mag licht.**
- Guidance: "To ensure appearance of being genuine and valid, no elaborate check is necessary at level
  Low." [B10]
- Voor elektronisch bewijs: "verification of electronic signatures by the authority issuing the evidence
  is a best practice." [B10]
- "Validity" omvat volgens de guidance "correctness of the information contained in the evidence,
  expiration date, and revocation/suspension state" [B10]. Op Low hoeft het bewijs alleen geldig te
  "lijken" ("appears to be valid"). Checks op verlies, diefstal, intrekking of verloopdatum zijn pas
  verplicht op Substantial ("steps have been taken to minimise the risk […] lost, stolen, suspended,
  revoked or expired evidence") [B2].

**Bestaan en binding (element 3): binding is op Low een aanname.**
- Gezaghebbende bron: "official evidence like register excerpts, identity documents or other officially
  provided evidence can serve as authoritative source of the person's existence." Plus: "Knowing the
  claimed identity exists includes ensuring that the person represented by the claimed identity is not
  deceased." [B10]
- Binding: de verordening zegt alleen "it may be assumed that the person claiming the identity is one and
  the same" [B2]. De guidance noemt manieren, met "can": "physical comparison (e.g., with a photographic
  identity document) can be carried out. Alternatively the applicant could be linked to an electronic
  authentication where attributes related to the authentication match the attributes held by the
  authoritative source" [B10].
- **Conclusie: op Low is binding niet vereist als controle.** Een vergelijking van gezicht en document
  is pas expliciet nodig in Substantial alternatief 2 ("the document appears to relate to the person
  presenting it") en in High 1(a) ("comparison of one or more physical characteristic of the person with
  an authoritative source") [B2].

### 1.4 Low naast Substantial en High

| Element | Low | Substantial (alternatief 1) | High (1a) |
|---|---|---|---|
| Bezit | "can be assumed" | "has been verified" | "has been verified" (foto- of biometrisch bewijs) |
| Echtheid | "can be assumed to be genuine" of bestaat volgens bron, "appears to be valid" | "checked to determine that it is genuine", of bestaat volgens bron | "checked to determine that it is valid according to an authoritative source" |
| Binding | "may be assumed […] one and the same" | "steps have been taken to minimise the risk that the person's identity is not the claimed identity" (alt. 2: "appears to relate to the person presenting it") | "comparison of one or more physical characteristic of the person with an authoritative source" |

Bron: [B2]. Een zijdelingse bevinding: Substantial eist letterlijk geen gezichtsvergelijking. DigiD haalt
Substantieel met alleen een NFC-scan, maar bindt die aan een bestaand account en het BSN (§1.6).
Onze Substantial (AA plus live gezicht via Regula) is een geldige route en sluit aan bij ETSI, maar niet
de enige.

### 1.5 Waar Low níet genoeg is (eIDAS 2 en de Business Wallet)

Geen enkel eIDAS 2-instrument gebruikt Low voor identiteitsvaststelling:
- EUDI-wallets: "European Digital Identity Wallets shall be provided under an electronic identification
  scheme with assurance level high." (art. 5a(11)) en ze moeten voldoen aan "assurance level high, in
  particular as applied to the requirements for identity proofing and verification" (art. 5a(5)(d)) [B1].
- PID-onboarding: "Member States shall enroll wallet users in accordance with the requirements relating to
  enrolment at assurance level high, as set out in Commission Implementing Regulation (EU) 2015/1502"
  (2024/2977 art. 3(7)) [B4]. Wallets moeten dat ondersteunen ("verify issuance, delivery and activation
  in compliance with assurance level high", 2024/2982 art. 4(3)(c)) [B6].
- Remote onboarding (2026/798, op grond van art. 5a(24)): substantial eID plus extra stappen tot high,
  getoetst aan ETSI TS 119 461 [B8]. Daarin: "The reference face image from the physical identity
  document shall be collected using near field communication and the process shall perform passive or
  active authentication of the chip on the physical identity document." (aanpassing VAL-8.3.3-22) [B8].
- QTSP's: identiteitsverificatie via EUDI-wallet of eID op "assurance level high", of methoden "with a
  high level of confidence" (art. 24(1a)) [B1], uitgewerkt in 2025/1566 via ETSI TS 119 461 [B7].
- Business Wallets (voorstel COM(2025) 838, in `regulation/`): onboarding via een gemachtigde met een eID
  "with regard to the assurance levels 'substantial' or 'high'" (art. 6(1)(e)). Toegang tot de wallet-unit
  vereist "at least the requirements for a substantial level of assurance" (bijlage, punt 1). De
  identiteitsvaststelling van operators buiten de EU loopt via de methoden van art. 24(1a) eIDAS (art.
  18(5)(a)) [B15]. De map `regulation/` is dus relevant, maar alleen als bevestiging: **Low speelt in de
  Business Wallet-verordening geen rol**.
- ETSI TS 119 461 kent geen niveau onder Baseline. "Baseline LoIP" betekent "reaching a substantial
  level of confidence", "Extended LoIP" betekent "reaching a high level of confidence" [B9]. Het woord
  "low" komt in de norm niet voor. Elke ETSI-usecase eist binding ("BIN-8.4.1-01X: The identity proofing
  process shall verify that the applicant is the legitimate holder of the authoritative evidence") [B9].
  2026/798 waarschuwt dat de gelijkwaardigheid tussen LoIP en de eIDAS-niveaus "has not been established"
  (overweging 6) [B8].

Low is dus alleen bruikbaar voor de eigen risicoafweging van een klant (laagrisicodiensten), niet om een
eIDAS 2-verplichting mee te halen.

### 1.6 De Nederlandse invulling

- Forum Standaardisatie, Handreiking betrouwbaarheidsniveaus v5 (2024) [B13]:
  - "Voor eIDAS geldt als eerste minimumeis voor de identiteitsverificatie dat de identiteitsgegevens die
    de gebruiker opgeeft, gecontroleerd kunnen worden in een basisregistratie. […] Maar, de gebruiker
    meldt zich niet fysiek in het registratieproces."
  - "Het is niet helemaal zeker dat de persoon die zich in de elektronische dienst meldt echt diegene is
    waar u als dienstverlener de identiteit van krijgt doorgegeven."
  - "DigiD (basis) is een voorbeeld van een middel op het niveau eIDAS laag."
  - Substantieel: "moet daadwerkelijk vastgesteld worden dat hij een geldig, officieel document bezit met
    identiteitsgegevens die gecontroleerd kunnen worden in een basisregistratie. Deze controle mag worden
    uitbesteed of op afstand plaatsvinden."
- Mapping van DigiD naar eIDAS:
  - BZK (toelichting bij de consultatieversie van de wijziging van de Regeling betrouwbaarheidsniveaus,
    2025): "In de eIDAS-verordening worden DigiD Basis en DigiD Midden aangeduid als
    betrouwbaarheidsniveau laag." [B14]
  - Logius (2020): Basis en Midden → Laag, Substantieel → Substantieel, Hoog → Hoog [B16].
  - Basis is "inloggen met enkel gebruikersnaam en wachtwoord", Midden is dat "met tweestapsverificatie"
    [B14].
- Substantieel bij DigiD is een NFC-scan zonder selfie, maar gebonden aan een bestaand account:
  - "Inloggen met de DigiD app die geactiveerd is met een ID-check. Om dit te voltooien moet de gebruiker
    eenmalig een identiteitsbewijs tegen zijn of haar smartphone houden." [B14]
  - "De DigiD app scant de NFC-chip van een identiteitsdocument en stelt bezit, echtheid en geldigheid vast
    op basis van het bij het account al bekende BSN." De sleutels om de chip te lezen komen uit
    "documentgegevens die met het BSN bij de BRP en het CRB zijn opgehaald" [B16].
  - Het verschil met onze Low: DigiD weet vooraf welk document bij welk BSN hoort (BRP en CRB) en kan
    geldigheid tegen een register controleren. Onze engine heeft geen vooraf bekende identiteit om de
    chip aan te binden.
- De Wet digitale overheid gebruikt voor overheidsdiensten alleen "substantieel en hoog" [B14]. Een
  Nederlandse admin leest "Laag" daarom als "niet genoeg voor de overheid".

## 2. Haalt onze definitie Low, schiet ze erover of tekort?

Wat de engine nu doet voor `low` (`flow.go`, `LevelRequirements`): "the subject holds genuine evidence: a
chip read whose Passive Authentication verifies (SOD signature, data-group hashes, CSCA chain)". Checks:
alleen `CheckNFCPassiveAuth`. Verder geldt op elke flow, los van het niveau, dat een verlopen document
wordt geweigerd (`flowComplianceFailure` in `sessions.go`, `DOC_EXPIRED`). De documentgegevens worden
uit de chip gehaald, niet uit wat de app meestuurt (`documentFromEvidence`, feature doc
`identity-proofing.md`).

| 2015/1502 §2.1.2 Low | Wat de engine doet | Oordeel |
|---|---|---|
| 1. Bezit "can be assumed" | De chip moet fysiek bij de telefoon (NFC). Openen vereist MRZ- of CAN-gegevens van het document. | Voldoet. Bezit is aannemelijk; geverifieerd is het niet (zie hieronder). |
| 1. Bewijs "recognised by the Member State" | Paspoort of ID-kaart met eMRTD-chip. De flow kan typen en landen beperken. Rijbewijs wordt nu geweigerd. | Voldoet, mits de geaccepteerde documenten erkend bewijs zijn. Dat is een beleidskeuze per flow, geen check. |
| 2. "can be assumed to be genuine" | Passive Authentication: SOD-handtekening, DG-hashes, keten naar CSCA. | Ruim boven Low: cryptografisch bewezen echt. Guidance: "best practice" [B10]. |
| 2. "appears to be valid" | Verlopen document geweigerd (DG1-vervaldatum), op elke flow. | Voldoet. Een check op intrekking, verlies of diefstal is pas Substantial. |
| 3. Identiteit bekend bij gezaghebbende bron | Het door de staat ondertekende chipdocument zelf. | Voldoet: "identity documents […] can serve as authoritative source" [B10]. |
| 3. Binding "may be assumed" | Geen gezichtscheck. Aanname: wie de chip heeft, is de houder. | Voldoet. Binding wordt op Low verondersteld, niet gecontroleerd. |

**Neemt een remote NFC-read "assumed to be in possession" mee?** Ja, met een kanttekening:
- Een NFC-read is een elektronische presentatie van het bewijs, en de guidance noemt "presenting the
  evidence physically or remotely (e.g., electronically)" als voorbeeld [B10].
- Passive Authentication bewijst echter alleen dat de data echt en ongewijzigd is. ICAO Doc 9303 deel 11
  §5.1: "Passive Authentication proves that the contents of the Document Security Object (SOD) and LDS
  are authentic and not changed. It does not prevent exact copying of the contactless IC's content or
  chip substitution." [B12]
- Wie eerder de chipdata van iemands paspoort heeft uitgelezen (een kloon, of een opname die een
  aangepaste app afspeelt), haalt PA. Zonder Active of Chip Authentication blijft bezit dus een aanname,
  en dat is precies wat Low toestaat.
- Pas Substantial ("verified to be in possession") vraagt om bescherming tegen kopieën. ETSI
  VAL-8.3.2-04X eist dan bescherming tegen "injection […] of a copy of a digital identity document" [B9].
  ICAO noemt AA en CA als de mechanismen: AA "Prevents copying the SOD and proves that it has been read
  from the authentic contactless IC", CA "Proves that the contactless IC has not been substituted" [B12].

**Overshoot?** Een beetje, op één punt en bewust. Low kan volgens de tekst ook met minder, bijvoorbeeld
een foto van het document met een visuele plausibiliteitscheck ("no elaborate check is necessary") [B10].
Chip plus PA is strenger, maar het is de goedkoopste route waarop de engine echtheid ook echt kan
vaststellen (de documentfoto wordt niet gecontroleerd en de app kan hem nog niet maken). Art. 1(3)
garandeert dat strenger nooit fout is [B2]. Prijs: wie geen NFC-telefoon of chipdocument heeft, kan Low
niet halen.

**Tekort?** Niet op de verordeningstekst. Drie randpunten:
1. *Niet overleden.* De guidance leest "known by an authoritative source that the claimed identity exists"
   als "includes ensuring that the person […] is not deceased" [B10]. De engine raadpleegt geen register
   (BRP). Een geldig, niet-verlopen document is de gangbare proxy. De guidance is niet bindend, maar dit is
   het enige punt waar een strenge lezer iets mist.
2. *Volledig eIDAS-niveau.* Art. 1(4) eist alle elementen van een niveau [B2]. Wij doen alleen §2.1.2
   (en §2.1.1 punt 3, gegevens verzamelen). Ons "Low" is "identiteitsvaststelling op Low", geen
   eIDAS-eID-middel op Low. Hetzelfde geldt voor Substantial.
3. *Chip Authentication (DG14) wordt niet geverifieerd* (bekend gat in `.ai/plans/identity-proofing.md`).
   Voor Low maakt dat niet uit: een gekloonde chip zonder DG15 haalt Low, en Low belooft niets over
   klonen. Het hoort wel in de toelichting voor de admin, niet in de lock.

## 3. Voorstel: wat zet de editor vast bij Low

| Instelling | Bij Low | Waarom |
|---|---|---|
| Stap "Document and chip" (`nfc_read`) | **Aan, vast** | Het bewijs (element 1) en het object van de echtheidscheck. |
| Check "The chip's data is genuine and unaltered" (`nfc.passive_auth`) | **Aan, vast** | Echtheid (element 2). `nfc_read` vereist deze check al (`checkStepBindings`). |
| Verlopen documenten weigeren | Altijd aan (engine-breed), alleen-lezen tonen | "appears to be valid" (element 2). Is al gedrag van elke flow. |
| Check "The chip is not a copy" (`nfc.chip_auth`, AA) | Vrij. Eventueel standaard aan, niet vast. | Niet vereist op Low. Kost de persoon niets (zelfde scan). Chips zonder AA-sleutel blijven gewoon op Low. |
| Gezichtsverificatie, liveness, gezichtsprovider | Vrij | Niet vereist op Low (binding "may be assumed"). |
| Documentfoto | Vrij | Niet nodig, en de engine controleert hem niet. |
| Geaccepteerde documenttypen en landen | Vrij | Bepaalt wat "recognised" bewijs is; beleid, geen niveau-eis. |

Dit is wat de engine vandaag al doet (`LevelRequirements` en `LEVEL_REQUIREMENTS` in
`frontend/src/lib/identity-proofing.ts`: `low: { documentAndChip: true }`). De beslissing in #305 is dus
geen codewijziging aan de engine, alleen de lock en de tekst in de editor.

## 4. Tekst voor de editor

Eén regel, in de stijl van de andere opties:

- EN: **"The person scans the chip in their passport or ID card with their phone. No selfie."**
- NL: "De persoon scant de chip van zijn paspoort of identiteitskaart met zijn telefoon. Geen selfie."

Optionele regel voor de admin, over wat Low niet bewijst:

- EN: "Low shows the document is genuine, not that the person scanning it is its owner or that the chip
  is the original."
- NL: "Laag toont aan dat het document echt is, niet dat de persoon die het scant de eigenaar is of dat
  de chip het origineel is."

Kortere variant als er weinig ruimte is: "Proves the document is genuine, not who is holding it."

## 5. Naamgeving

- **Inconsistent.** De editor toont "None", "Low", "eIDAS Substantial". Low is net zo goed een eIDAS-niveau
  (art. 8(1): "low, substantial and/or high") [B1]. Of beide krijgen "eIDAS", of geen van beide.
- **Precisie.** Beide labels claimen alleen het identiteitsvaststellingsdeel (2015/1502 §2.1.2), geen
  genotificeerd eID-middel en geen conformiteitsbeoordeling (art. 1(4)) [B2]. "eIDAS Substantial" als los
  label kan klinken als meer dan we leveren. Voorstel: veldnaam "Identity proofing level" met de
  opties "None", "Low", "Substantial" en één hulptekst: "Levels as eIDAS defines them for identity
  proofing (Implementing Regulation (EU) 2015/1502, section 2.1.2)." Dat houdt de eIDAS-verankering
  zonder per optie te overclaimen.
- **Nederlands.** "Laag" is in Nederland DigiD Basis/Midden en volstaat niet voor diensten onder de Wdo
  [B14]. Admins die DigiD kennen, weten dat een NFC-scan daar Substantieel oplevert [B16]. De hulptekst
  (§4) moet uitleggen waarom onze scan Low is: geen binding aan een vooraf bekende persoon.
- **Niet doen.** ETSI-termen ("Baseline", "Extended") gebruiken: ETSI heeft geen equivalent voor Low, en
  Baseline is al substantial [B9]. Hernoemen naar iets als "Document check" kan wel, maar verliest de
  eIDAS-verankering die klanten in hun eigen risicoanalyse nodig hebben.

## Bronnen

Primaire teksten zijn opgehaald via de Publications Office (cellar) of de uitgever zelf, en citaten zijn
tegen die tekst gecontroleerd.

- [B1] Verordening (EU) nr. 910/2014 (eIDAS), geconsolideerd 18.10.2024 (incl. 2024/1183): art. 5a(5)(d),
  5a(11), 5a(24), 8, 24(1a)-(1c). https://eur-lex.europa.eu/legal-content/EN/TXT/?uri=CELEX:02014R0910-20241018
  (ELI: http://data.europa.eu/eli/reg/2014/910/oj)
- [B2] Uitvoeringsverordening (EU) 2015/1502, art. 1 en bijlage §2.1.1-2.1.2 (EN).
  https://eur-lex.europa.eu/eli/reg_impl/2015/1502/oj
  - [B2-NL] Dezelfde, Nederlandse taalversie. https://eur-lex.europa.eu/legal-content/NL/TXT/?uri=CELEX:32015R1502
- [B3] Uitvoeringsverordening (EU) 2022/960 (correctie Tsjechische taalversie van 2015/1502).
  https://eur-lex.europa.eu/eli/reg_impl/2022/960/oj
- [B4] Uitvoeringsverordening (EU) 2024/2977 (PID en EAA), overweging 8 en art. 3(7).
  https://eur-lex.europa.eu/eli/reg_impl/2024/2977/oj
- [B5] Uitvoeringsverordening (EU) 2024/2979 (integriteit en kernfunctionaliteit wallets): high voor
  cryptografische operaties. https://eur-lex.europa.eu/eli/reg_impl/2024/2979/oj
- [B6] Uitvoeringsverordening (EU) 2024/2982 (protocollen en interfaces), art. 4(3)(c).
  https://eur-lex.europa.eu/eli/reg_impl/2024/2982/oj
- [B7] Uitvoeringsverordening (EU) 2025/1566 (identiteitsverificatie QTSP, verwijst naar ETSI TS 119 461
  V2.1.1, van toepassing vanaf 19 augustus 2027). https://eur-lex.europa.eu/eli/reg_impl/2025/1566/oj
- [B8] Uitvoeringsverordening (EU) 2026/798 (remote onboarding EUDI-wallet), overweging 6 en bijlage
  (VAL-8.3.3-22). https://eur-lex.europa.eu/eli/reg_impl/2026/798/oj
- [B9] ETSI TS 119 461 V2.1.1 (2025-02), §1, §3.1 (definities Baseline/Extended LoIP), §4.2, VAL-8.3.2,
  BIN-8.4.1, §9.2.3.4. https://www.etsi.org/deliver/etsi_ts/119400_119499/119461/02.01.01_60/ts_119461v020101p.pdf
- [B10] eIDAS Cooperation Network, "Guidance for the application of the levels of assurance which support
  the eIDAS Regulation" (versie 2021), §2.1.2. Pagina:
  https://ec.europa.eu/digital-building-blocks/sites/spaces/EIDCOMMUNITY/pages/40044784/Guidance+documents ;
  bestand: https://ec.europa.eu/digital-building-blocks/sites/download/attachments/40044784/LoA%20guidance.docx?version=1&modificationDate=1625231711620&api=v2
- [B11] ENISA, "Remote ID Proofing: Analysis of methods to carry out identity proofing remotely" (maart
  2021). Voegt voor Low niets primairs toe: verwijst voor de interpretatie naar [B10].
  https://www.enisa.europa.eu/sites/default/files/publications/ENISA%20Report%20-%20Remote%20ID%20Proofing.pdf
- [B12] ICAO Doc 9303, Part 11 "Security Mechanisms for MRTDs", 8e editie 2021, tabel 1 en §5.1.
  https://www.icao.int/sites/default/files/publications/DocSeries/9303_p11_cons_en.pdf
- [B13] Forum Standaardisatie, "Betrouwbaarheidsniveaus voor digitale dienstverlening", versie 5 (2024),
  §3 (eIDAS-niveaus). https://forumstandaardisatie.nl/sites/default/files/fs-handreiking-betrouwbaarheidsniveus-v5.pdf
- [B14] BZK, consultatieversie "Regeling […] tot wijziging van de Regeling betrouwbaarheidsniveaus
  authenticatie elektronische dienstverlening" met toelichting (2025), DigiD-niveaus en voetnoot 2.
  https://www.internetconsultatie.nl/regelingbetrouwbaarheidsniveaus/document/13736
- [B15] Voorstel Verordening European Business Wallets, COM(2025) 838 final, art. 6(1)(e), 18(5)(a) en
  bijlage punt 1. In deze repo: `regulation/COM_2025_838_act.md`, `regulation/COM_2025_838_annex.md`.
- [B16] Logius, "Functionele beschrijving DigiD app", versie 0.2 (10-06-2020), §2.4 en §3.3-3.4.
  https://logius.nl/sites/default/files/bestanden/website/Functionele%20beschrijving%20DigiD%20app%20v0.2%20def_0.pdf
- Code (op `origin/main`): `backend/internal/proofingengine/flow/flow.go` (`LevelRequirements`,
  `checkStepBindings`), `backend/internal/proofingengine/sessions.go` (`flowComplianceFailure`,
  `DOC_EXPIRED`), `frontend/src/lib/identity-proofing.ts` (`LEVEL_REQUIREMENTS`),
  `.ai/features/identity-proofing.md`.

Niet geverifieerd: of de wijziging van de Regeling betrouwbaarheidsniveaus [B14] ongewijzigd is
vastgesteld (de DigiD-beschrijvingen komen uit de toelichting bij de consultatieversie). De werking van de
DigiD ID-check komt uit een Logius-document uit 2020 [B16] en kan sindsdien veranderd zijn.
