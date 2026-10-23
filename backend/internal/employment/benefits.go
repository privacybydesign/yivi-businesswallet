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

// classify tells what a relation is from its printed payer and number.
func classify(employer, payrollTaxNumber string) Kind {
	uwvBenefit := slices.ContainsFunc(uwvBenefits, func(b string) bool { return strings.EqualFold(b, employer) })
	switch {
	case uwvBenefit && slices.Contains(uwvBenefitPayrollTaxNumbers, payrollTaxNumber):
		return KindBenefit
	case reSickViaEmployer.MatchString(employer) && !uwvBenefit:
		return KindSickViaEmployer
	default:
		return KindEmployment
	}
}
