package webtools

import "testing"

func TestPageText(t *testing.T) {
	in := `<!doctype html><html><head><title>T</title><style>p{}</style></head>
<body><script>var x = "<p>";</script><h1>Acme &amp; Cie</h1><p>85   collaborateurs</p>
<ul><li>Account Executive</li><li>Sales Manager</li></ul></body></html>`
	want := "Acme & Cie\n85 collaborateurs\nAccount Executive\nSales Manager"
	if got := pageText(in); got != want {
		t.Fatalf("pageText =\n%q\nattendu\n%q", got, want)
	}
	if got := pageText("  texte brut  "); got != "texte brut" {
		t.Fatalf("texte brut : %q", got)
	}
}

func TestURLKey(t *testing.T) {
	same := []string{"https://acme.test/careers", "http://www.acme.test/careers/", "https://ACME.test/careers#x"}
	for _, u := range same {
		if urlKey(u) != "acme.test/careers" {
			t.Errorf("urlKey(%q) = %q", u, urlKey(u))
		}
	}
	if urlKey("acme.test") != "" || urlKey("https://acme.test/?a=1") != "acme.test?a=1" {
		t.Error("URL invalide ou requête")
	}
}
