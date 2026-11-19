package employment

import (
	"regexp"
	"slices"
	"strings"
)

// The benefits UWV pays, as the Verzekeringsbericht prints them under
// "Werkgever/Instantie", and the loonheffingennummers it pays them under:
// HDN's Rekenregels Inkomensbepaling Loondienst 8.1.1 and 9.0.0, Appendix 5,
// and its "Bijlage Loonheffingennummers voor Uitkering via het UWV". A
// relation is a UWV benefit only when both match, as IBL decides it. The
// descriptions compare without case: 8.1.1 writes "WW-Uitkering", 9.0.0
// "WW-uitkering".
var (
	uwvBenefits = []string{
		"Ziektewet- of WAZO-Uitkering van UWV",
		"WAO-Uitkering van UWV",
		"WW-Uitkering van UWV",
		"WAZ-Uitkering van UWV",
		"WAO- en Wajong-Uitkering van UWV",
		"WIA (IVA)-Uitkering van UWV",
		"WIA (WGA)-Uitkering van UWV",
		"Toeslag bij Uitkering (TW) van UWV",
		"IOW-Uitkering van UWV",
		"UWV; Loondoorbetaling bij faillissement",
	}
	uwvBenefitPayrollTaxNumbers = []string{
		"810220350L02", "810220350L04", "810220350L20", "810220350L23", "810220350L26", "810220350L53",
	}
	// reSickViaEmployer is a sickness or WAZO benefit the employer pays:
	// "Ziektewet- of WAZO-Uitkering van <naam van de Werkgever>".
	reSickViaEmployer = regexp.MustCompile(`(?i)^Ziektewet- of WAZO-Uitkering van (.+)$`)
)

// uwvPayer is who a UWV benefit names as its payer once classified.
const uwvPayer = "UWV"

// classify tells what r is from its printed payer and number, and drops the
// benefit's name: "WIA", "Wajong" or "Ziektewet- of WAZO-Uitkering" tells of
// sickness, disability or pregnancy (data concerning health, GDPR Art. 9),
// which the occupational status does not need. A UWV benefit's payer is
// UWV, without its number; sick pay through the employer is that employer's.
func classify(r *Relation) {
	uwvBenefit := slices.ContainsFunc(uwvBenefits, func(b string) bool { return strings.EqualFold(b, r.Employer) })
	sickVia := reSickViaEmployer.FindStringSubmatch(r.Employer)
	switch {
	case uwvBenefit:
		r.Kind = KindEmployment
		if slices.Contains(uwvBenefitPayrollTaxNumbers, r.PayrollTaxNumber) {
			r.Kind = KindBenefit
		}
		r.Employer, r.PayrollTaxNumber = uwvPayer, ""
	case sickVia != nil:
		r.Kind, r.Employer = kindSickViaEmployer, sickVia[1]
	default:
		r.Kind = KindEmployment
	}
}
