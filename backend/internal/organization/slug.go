package organization

import (
	"errors"
	"regexp"
)

var (
	ErrInvalidSlug  = errors.New("organization slug is not URL-safe")
	ErrReservedSlug = errors.New("organization slug is reserved")
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

var reservedSlugs = map[string]struct{}{
	"login":       {},
	"admin":       {},
	"invite":      {},
	"invitations": {},
}

func ValidateSlug(slug string) error {
	if !slugPattern.MatchString(slug) {
		return ErrInvalidSlug
	}
	if _, ok := reservedSlugs[slug]; ok {
		return ErrReservedSlug
	}
	return nil
}
