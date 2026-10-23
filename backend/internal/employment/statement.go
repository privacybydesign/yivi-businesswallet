// Package employment reads and checks the Verzekeringsbericht: the overview
// of a person's employment and benefits that UWV (Uitvoeringsinstituut
// werknemersverzekeringen) seals, downloaded from Mijn UWV. Its signature is
// checked with package diploma's verifier under trust.UWV.
package employment

import "time"

// currentMonths is how far back from the statement's month a relation's last
// reported month may lie and still be current. Employers file a month's
// payroll by the end of the next one, so on 22 October September is the
// newest month, and early in a month it can be August.
const currentMonths = 2

// Statement is what a Verzekeringsbericht prints that the wallet uses.
type Statement struct {
	// Initials are the holder's initials as printed, one per given name, e.g.
	// ["J", "Th"] for "J.Th. de Vries".
	Initials []string
	// Surname is the holder's surname as printed, the salutation and initials
	// dropped: "de Vries", or a married name such as "Jansen-de Vries".
	Surname string
	// DateOfBirth is the printed "Geboortedatum".
	DateOfBirth time.Time
	// Date is the printed "Datum": the day UWV made the statement.
	Date time.Time
	// Relations are the employment and benefit relations in the "Loongegevens"
	// part, one per employer and contract.
	Relations []Relation
}

// Current returns the relations still running when UWV made the statement,
// as HDN's Inkomensbepaling Loondienst (§3.1) reads it: a job with hours
// worked in a month at most currentMonths before the statement's month, or a
// benefit paid in one. Not those reporting the statement's newest month:
// someone who stopped in June would otherwise have their June job read as
// current. A month paid without hours (a final payment, an allowance, an
// on-call contract without shifts) is no work.
func (st *Statement) Current() []Relation {
	cutoff := time.Date(st.Date.Year(), st.Date.Month()-currentMonths, 1, 0, 0, 0, 0, time.UTC)
	current := []Relation{}
	for _, r := range st.Relations {
		last := r.To
		if r.Kind == KindEmployment {
			last = r.Worked
		}
		if !last.Before(cutoff) {
			current = append(current, r)
		}
	}
	return current
}

// Employed reports whether the holder has a current relation that is not a
// benefit: a job, or a sickness benefit their employer pays.
func (st *Statement) Employed() bool {
	for _, r := range st.Current() {
		if r.Kind != KindBenefit {
			return true
		}
	}
	return false
}

// Kind is what a relation is.
type Kind string

const (
	// KindEmployment is a job.
	KindEmployment Kind = "employment"
	// KindBenefit is a benefit UWV pays (isUWVBenefit).
	KindBenefit Kind = "benefit"
	// KindSickViaEmployer is a sickness or WAZO benefit paid through the
	// employer: the holder still has the job.
	KindSickViaEmployer Kind = "sick_via_employer"
)

// Relation is one income relation: an employer or a paying body (UWV)
// under one contract, over the months the statement reports it. The wages
// per month are read past and never kept.
type Relation struct {
	// Employer is "Werkgever/Instantie".
	Employer string
	// PayrollTaxNumber is "Loonheffingennummer", which tells two employers of
	// the same name apart.
	PayrollTaxNumber string
	// ContractForm is "Contractvorm", empty when the statement prints none.
	ContractForm string
	// From and To are the first day of the first and the last day of the
	// last month reported.
	From, To time.Time
	// Worked is the last day of the newest month with hours worked; zero
	// when no month has any.
	Worked time.Time
	// Kind is what the relation is (classify).
	Kind Kind
}
