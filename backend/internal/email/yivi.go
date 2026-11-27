package email

import _ "embed"

// yiviLogo heads Yivi's own notices, made from the frontend's
// yivi-logo-horizontal.svg at twice the mail's logo height.
//
//go:embed yivi-logo.png
var yiviLogo []byte

const yiviWordmark = "Yivi"
