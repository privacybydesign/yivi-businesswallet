package employment

import (
	"regexp"
	"slices"
)

// The benefits UWV pays, as the Verzekeringsbericht prints them under
// "Werkgever/Instantie", and the loonheffingennummers it pays them under:
// HDN's Rekenregels Inkomensbepaling Loondienst 8.0 (17 June 2024),
// Appendix 5. A relation is a UWV benefit only when both match exactly, as
// IBL decides it.
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
	}
	uwvBenefitPayrollTaxNumbers = []string{
		"810220350L02", "810220350L04", "810220350L20", "810220350L23", "810220350L26",
	}
	// reSickViaEmployer is a sickness or WAZO benefit the employer pays:
	// "Ziektewet- of WAZO-Uitkering van <naam van de Werkgever>".
	reSickViaEmployer = regexp.MustCompile(`^Ziektewet- of WAZO-Uitkering van (.+)$`)
)

// classify tells what a relation is from its printed payer and number.
func classify(employer, payrollTaxNumber string) Kind {
	switch {
	case slices.Contains(uwvBenefits, employer) && slices.Contains(uwvBenefitPayrollTaxNumbers, payrollTaxNumber):
		return KindBenefit
	case reSickViaEmployer.MatchString(employer) && !slices.Contains(uwvBenefits, employer):
		return KindSickViaEmployer
	default:
		return KindEmployment
	}
}
