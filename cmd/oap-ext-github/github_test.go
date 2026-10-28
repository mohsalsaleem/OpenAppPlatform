package main

import (
	"testing"

	extensionv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/extension/v1"
)

func TestCIState(t *testing.T) {
	green, red, pending, none := extensionv1.CiState_CI_STATE_GREEN, extensionv1.CiState_CI_STATE_RED, extensionv1.CiState_CI_STATE_PENDING, extensionv1.CiState_CI_STATE_NONE
	for _, c := range []struct {
		runs, statuses []string
		want           extensionv1.CiState
	}{
		{[]string{"success", "skipped", "neutral"}, nil, green},
		{[]string{"success", "failure"}, nil, red},
		{[]string{"success", "pending"}, nil, pending},
		{[]string{"pending", "cancelled"}, nil, red},
		{nil, []string{"success"}, green},
		{nil, []string{"error"}, red},
		{[]string{"success"}, []string{"pending"}, pending},
		{[]string{"skipped"}, nil, none},
		{nil, nil, none},
	} {
		if got := ciState(c.runs, c.statuses); got != c.want {
			t.Errorf("runs %v statuses %v: %v, want %v", c.runs, c.statuses, got, c.want)
		}
	}
}

func TestApproved(t *testing.T) {
	r := func(u, s string) review { return review{u, s} }
	for _, c := range []struct {
		reviews []review
		want    bool
	}{
		{[]review{r("a", "APPROVED")}, true},
		{[]review{r("a", "COMMENTED")}, false},
		{[]review{r("a", "APPROVED"), r("b", "CHANGES_REQUESTED")}, false},
		{[]review{r("b", "CHANGES_REQUESTED"), r("b", "APPROVED")}, true},
		{[]review{r("a", "APPROVED"), r("a", "DISMISSED")}, false},
		{[]review{r("a", "APPROVED"), r("a", "COMMENTED")}, true},
		{nil, false},
	} {
		if got := approved(c.reviews); got != c.want {
			t.Errorf("%v: %v, want %v", c.reviews, got, c.want)
		}
	}
}

func TestOwnerName(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/acme/shop": "acme/shop", "https://github.com/acme/shop.git": "acme/shop",
		"git@github.com:acme/shop.git": "acme/shop", "ssh://git@github.com/acme/shop": "acme/shop", "acme/shop": "acme/shop",
	} {
		if got, err := ownerName(in); err != nil || got != want {
			t.Errorf("%s: %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"https://github.com/acme", "not a repo", ""} {
		if _, err := ownerName(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
