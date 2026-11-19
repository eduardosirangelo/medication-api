package medication

import (
	"errors"
	"strings"
)

// NDC is a National Drug Code in its normalized 11-digit 5-4-2 form, stored
// without hyphens. The zero value means "no NDC".
//
// Labelers publish 10-digit NDCs in three layouts (4-4-2, 5-3-2, 5-4-1);
// billing standards use 11 digits, obtained by left-padding the short segment
// with a zero. Normalizing makes the same package compare equal however it
// was typed.
type NDC string

var ErrNDCFormat = errors.New("must be an NDC: 10 digits hyphenated as 4-4-2, 5-3-2 or 5-4-1, or 11 digits as 5-4-2 or unhyphenated")

var ndcSegmentTargets = [3]int{5, 4, 2}

func ParseNDC(s string) (NDC, error) {
	s = strings.TrimSpace(s)
	if len(s) == 11 && allDigits(s) {
		return NDC(s), nil
	}
	segments := strings.Split(s, "-")
	if len(segments) != 3 || !hasValidNDCLayout(segments) {
		// An unhyphenated 10-digit code is ambiguous: we can't know which
		// segment to pad, so it is rejected rather than guessed.
		return "", ErrNDCFormat
	}
	var b strings.Builder
	for i, seg := range segments {
		b.WriteString(strings.Repeat("0", ndcSegmentTargets[i]-len(seg)))
		b.WriteString(seg)
	}
	return NDC(b.String()), nil
}

func hasValidNDCLayout(segments []string) bool {
	lengths := [3]int{}
	for i, seg := range segments {
		if !allDigits(seg) {
			return false
		}
		lengths[i] = len(seg)
	}
	switch lengths {
	case [3]int{4, 4, 2}, [3]int{5, 3, 2}, [3]int{5, 4, 1}, [3]int{5, 4, 2}:
		return true
	}
	return false
}

func (n NDC) Hyphenated() string {
	if len(n) != 11 {
		return string(n)
	}
	return string(n[:5]) + "-" + string(n[5:9]) + "-" + string(n[9:])
}

// RxCUI is an RxNorm concept unique identifier. It is an identifier, not a
// number, so it stays a string. The zero value means "no RxCUI".
type RxCUI string

const maxRxCUIDigits = 10

var ErrRxCUIFormat = errors.New("must be an RxNorm concept ID: 1 to 10 digits")

func ParseRxCUI(s string) (RxCUI, error) {
	s = strings.TrimSpace(s)
	if len(s) > maxRxCUIDigits || !allDigits(s) {
		return "", ErrRxCUIFormat
	}
	return RxCUI(s), nil
}

// Route is the route of administration. Canonical values are lowercase; the
// zero value means "not specified".
type Route string

const (
	RouteOral          Route = "oral"
	RouteSublingual    Route = "sublingual"
	RouteTopical       Route = "topical"
	RouteTransdermal   Route = "transdermal"
	RouteInhalation    Route = "inhalation"
	RouteNasal         Route = "nasal"
	RouteOphthalmic    Route = "ophthalmic"
	RouteOtic          Route = "otic"
	RouteRectal        Route = "rectal"
	RouteVaginal       Route = "vaginal"
	RouteIntravenous   Route = "intravenous"
	RouteIntramuscular Route = "intramuscular"
	RouteSubcutaneous  Route = "subcutaneous"
)

var Routes = []Route{
	RouteOral, RouteSublingual, RouteTopical, RouteTransdermal, RouteInhalation,
	RouteNasal, RouteOphthalmic, RouteOtic, RouteRectal, RouteVaginal,
	RouteIntravenous, RouteIntramuscular, RouteSubcutaneous,
}

var routesByName = indexByName(Routes)

func ParseRoute(s string) (Route, bool) {
	r, ok := routesByName[strings.ToLower(strings.TrimSpace(s))]
	return r, ok
}

func indexByName[T ~string](vals []T) map[string]T {
	m := make(map[string]T, len(vals))
	for _, v := range vals {
		m[string(v)] = v
	}
	return m
}
