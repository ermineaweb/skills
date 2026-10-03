package websearch_test

import (
	"testing"

	"skills/services/websearch"
)

func TestDomain(t *testing.T) {
	cases := map[string]string{
		"https://acme-saas.test/careers":     "acme-saas.test",
		"http://WWW.Acme-SaaS.test:8080/a?b": "acme-saas.test",
		"https://blog.acme-saas.test/":       "blog.acme-saas.test",
		"acme-saas.test/careers":             "",
		"javascript:void(0)":                 "",
		"":                                   "",
	}
	for in, want := range cases {
		if got := websearch.Domain(in); got != want {
			t.Errorf("Domain(%q) = %q, attendu %q", in, got, want)
		}
	}
}
